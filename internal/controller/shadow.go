package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dbv1 "github.com/wwwdotsheng/db-migration-operator/api/v1"
	"github.com/wwwdotsheng/db-migration-operator/internal/adapter"
)

// 影子预演（Job 方案）—— 与 Reconcile 状态机分开。
//
// 这里【不出现任何 driver 名字、shell 脚本、镜像名、uid】——
// 那些都由 driver 通过 adapter.ShadowValidator.ShadowSpec 提供。

// ────────────────────────────── 影子预演（Job 方案）──────────────────────────────
//
// 设计：把「需要数据库客户端工具」的活外置成一个 Job。
//
//	Operator 镜像保持 distroless（最小攻击面），不装 pg_dump / mysqldump；
//	Job 的镜像和脚本由 driver 自己提供（adapter.ShadowValidator.ShadowSpec）——
//	因为不同数据库的 dump 工具、临时实例启动方式都不一样。
//
// ★ controller 只负责：创建 Job、看状态、读失败原因。
//
//	它里面【不出现任何 driver 名字，也不出现任何 shell 脚本】。
//
// 代价（要能讲清）：
//   · Operator 需要有创建 Job 的权限 —— 这是权限放大，
//     用独立的 ServiceAccount + 只允许白名单镜像来收敛
//   · 比在进程内克隆慢（起 Pod + 拉镜像）
//
// ★ 边界：只验证【结构与语法】，验证不了性能 ——
// 影子实例里是空表，大表 ALTER 的真实耗时测不出来。

const (
	// shadowJobTimeout 是 Job 的总超时（produce + 试跑）。
	shadowJobTimeout = 5 * time.Minute
	// shadowJobTTL 是 Job 结束后保留多久再被 GC。
	shadowJobTTL = 10 * time.Minute
	// shadowJobPollInterval 是 Job 还在跑时的重新入队间隔。
	shadowJobPollInterval = 10 * time.Second
)

// ValidationError 表示影子预演失败。
type ValidationError struct {
	Reason string
}

func (e *ValidationError) Error() string { return "影子预演失败: " + e.Reason }

// imageRepository 读部署环境给的镜像仓库前缀。
//
// ★ 为什么放在 controller 而不是 driver：
//
//	「用哪个镜像」是 driver 的事（postgres:15-alpine）；
//	「从哪个仓库拉」是部署环境的事（harbor.internal/dockerhub/）。
//	两者分开，同一份代码既能跑在能直连公网的集群，也能跑在内网私有仓库。
//
// 空值 = 用镜像的原始名字。
func imageRepository() string {
	repo := os.Getenv("IMAGE_REPOSITORY")
	if repo != "" && !strings.HasSuffix(repo, "/") {
		repo += "/"
	}
	return repo
}

// resolveImage 把 driver 给的相对镜像名拼成完整名。
func resolveImage(image string) string {
	return imageRepository() + image
}

// shadowJobName 生成确定性的 Job 名，便于幂等（重复 reconcile 不会建出多个）。
//
// ★ 必须把 generation 也算进去：
//
//	只按 namespace/name 算的话，改了 CR 的 applySql 之后 Job 名不变 ——
//	而旧 Job 还在（TTL 10 分钟），reconcile 会看到它的 Succeeded > 0，
//	误判「预演通过」，实际用的是【过期的预演结果】。
//	generation 在 spec 变更时会 +1，所以名字跟着变，旧 Job 被自然淘汰。
func shadowJobName(migration *dbv1.SchemaMigration) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s/%s/%d",
		migration.Namespace, migration.Name, migration.Generation)))
	return "shadow-" + hex.EncodeToString(sum[:6])
}

// shadowValidate 用 Job 在临时实例上预演一次迁移。
//
// 返回 done=false 表示 Job 还在跑 —— 调用方应该退出本次 reconcile，
// 等 Job 状态变化（Owns 会唤醒）或者超时后重新入队。
func (r *SchemaMigrationReconciler) shadowValidate(
	ctx context.Context, migration *dbv1.SchemaMigration,
	persistent adapter.Driver, cfg adapter.Config, serverVersion string,
) (bool, error) {
	// ★ 能力由 driver 自己声明 —— controller 里不出现任何 driver 名字。
	//   实现了 ShadowValidator = 支持预演；没实现就明确拒绝（不静默跳过）。
	sv, ok := persistent.(adapter.ShadowValidator)
	if !ok {
		return false, &ValidationError{
			Reason: fmt.Sprintf("driver %q 未实现影子预演（没有实现 adapter.ShadowValidator）", migration.Spec.Driver),
		}
	}
	spec, err := sv.ShadowSpec(cfg, migration.Spec.ApplySql, serverVersion)
	if err != nil {
		return false, &ValidationError{Reason: fmt.Sprintf("组装预演容器规格失败: %v", err)}
	}

	name := shadowJobName(migration)
	key := client.ObjectKey{Namespace: migration.Namespace, Name: name}

	var job batchv1.Job
	err = r.Get(ctx, key, &job)
	if apierrors.IsNotFound(err) {
		if cerr := r.createShadowJob(ctx, migration, spec, name); cerr != nil {
			return false, fmt.Errorf("创建预演 Job 失败: %w", cerr)
		}
		return false, nil
	}
	if err != nil {
		return false, err
	}

	switch {
	case job.Status.Succeeded > 0:
		return true, nil

	case job.Status.Failed > 0 || isJobDeadlineExceeded(&job):
		// ★ 失败原因从 Pod 的 termination message 拿 ——
		//   比读日志简单（不用 client-go 的 Clientset，也不用 pods/log 权限）
		return false, &ValidationError{Reason: r.shadowJobFailure(ctx, &job)}

	default:
		// 还在跑
		return false, nil
	}
}

func isJobDeadlineExceeded(job *batchv1.Job) bool {
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobFailed && c.Reason == "DeadlineExceeded" {
			return true
		}
	}
	return false
}

// shadowJobFailure 从 Job 的 Pod 里取出失败原因（termination message）。
func (r *SchemaMigrationReconciler) shadowJobFailure(ctx context.Context, job *batchv1.Job) string {
	var pods corev1.PodList
	if err := r.List(ctx, &pods,
		client.InNamespace(job.Namespace),
		client.MatchingLabels(job.Spec.Template.Labels),
	); err != nil {
		return "Job 失败（且取不到 Pod：检查 RBAC 是否有 pods 的 list 权限）"
	}
	for i := range pods.Items {
		for _, cs := range pods.Items[i].Status.ContainerStatuses {
			if cs.State.Terminated != nil && cs.State.Terminated.Message != "" {
				return summarizeFailure(cs.State.Terminated.Message, job.Name)
			}
		}
	}
	return "Job 失败，但拿不到容器输出（检查 RBAC 是否有 pods 的 get 权限）"
}

// summarizeFailure 把容器的整段输出压成一行摘要。
//
// ★ 为什么必须压：
//
//	status.message 会写进 etcd 对象，整段 stdout（几十行）会
//	撑大对象、kubectl 输出难读、而且真正有用的那行被淹掉。
//	完整输出去 Job 日志里看 —— 所以摘要里带上怎么查。
func summarizeFailure(raw, jobName string) string {
	lines := strings.Split(raw, "\n")

	// 优先找脚本自己标的失败行（形如 "SHADOW-VALIDATION-FAILED: 试跑失败"）
	for i, l := range lines {
		if !strings.Contains(l, "SHADOW-VALIDATION-FAILED:") {
			continue
		}
		summary := strings.TrimSpace(l)
		// ★ 从标记行【之后】往下找第一条非空内容 ——
		//   那通常才是数据库给的真报错（"relation xxx does not exist"）。
		//   从头扫会抓到 "[1/4] initdb..." 这种进度行，没信息量。
		for _, l2 := range lines[i+1:] {
			if t := strings.TrimSpace(l2); t != "" {
				summary += " | " + t
				break
			}
		}
		return clip(summary, 300) + "（完整日志: kubectl logs job/" + jobName + "）"
	}

	// 兜底：取最后一行非空内容
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			return clip(t, 300) + "（完整日志: kubectl logs job/" + jobName + "）"
		}
	}
	return "Job 失败，输出为空（完整日志: kubectl logs job/" + jobName + "）"
}

// clip 按【字节】截断，但不会把一个 UTF-8 字符切一半。
func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// createShadowJob 组装并创建预演 Job。
// shadowPodSecurityContext 把 driver 声明的 uid 转成 Pod 级 securityContext。
func shadowPodSecurityContext(spec adapter.ShadowSpec) *corev1.PodSecurityContext {
	if spec.RunAsUser == nil && spec.FSGroup == nil {
		return nil
	}
	return &corev1.PodSecurityContext{
		RunAsUser:  spec.RunAsUser,
		RunAsGroup: spec.RunAsUser,
		FSGroup:    spec.FSGroup,
	}
}

func (r *SchemaMigrationReconciler) createShadowJob(
	ctx context.Context, migration *dbv1.SchemaMigration, spec adapter.ShadowSpec, name string,
) error {
	backoff := int32(0)
	deadline := int64(shadowJobTimeout.Seconds())
	ttl := int32(shadowJobTTL.Seconds())
	labels := map[string]string{
		"app.kubernetes.io/name": "db-migration-operator",
		"shadow-of":              migration.Name,
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: migration.Namespace, Labels: labels},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoff, // ★ 不重试：失败就是失败，重试结果一样
			ActiveDeadlineSeconds:   &deadline,
			TTLSecondsAfterFinished: &ttl, // ★ 跑完自动 GC，不留垃圾
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					// ★ uid 由 driver 给（不同镜像里那个非 root 用户的 uid 不一样）。
					//   这里【不能】用 RunAsNonRoot: true —— 官方数据库镜像
					//   大多没有 USER 指令（默认 root），kubelet 会直接拒绝启动。
					SecurityContext: shadowPodSecurityContext(spec),
					Containers: []corev1.Container{{
						Name:    "shadow",
						Image:   resolveImage(spec.Image),
						Command: []string{"/bin/sh", "-c"},
						Args:    []string{spec.Script},
						Env:     spec.Env,
						// ★ 失败原因走日志尾部（脚本打到 stderr），
						//   因为非 root 写不了 /dev/termination-log（实测）。
						TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
					}},
				},
			},
		},
	}

	// ★ OwnerReference：CR 删了，Job 自动跟着删
	if err := ctrl.SetControllerReference(migration, job, r.Scheme); err != nil {
		return err
	}
	if err := r.Create(ctx, job); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

// ptr 返回 v 的指针 —— 用于 corev1 里那些 *int64 / *int32 字段。
func ptr[T any](v T) *T { return &v }
