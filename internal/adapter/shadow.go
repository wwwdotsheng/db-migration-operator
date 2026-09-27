package adapter

import corev1 "k8s.io/api/core/v1"

// ShadowSpec 描述「怎么在一个临时环境里预演一次迁移」。
//
// ★ 为什么放在 adapter 而不是 controller：
//
//	影子预演是 driver 特定的 —— 不同数据库的 dump 工具、临时实例启动方式、
//	镜像都不一样（PG 用 pg_dump + initdb，MySQL 用 mysqldump + mysqld --initialize）。
//	放在 controller 里就得写 if driver == "xxx" 的分支，
//	违反「加新 driver = 实现接口 + 注册一行，controller 零改动」的既有设计。
type ShadowSpec struct {
	// Image 是跑预演的容器镜像。
	// ★ 它必须【同时】含 server 和客户端 —— 因为预演要在同一个 Pod 里
	//   起一个临时实例，然后往它导入 schema 并试跑迁移。
	Image string

	// Script 是容器里执行的 shell 脚本（由 /bin/sh -c 跑）。
	// 约定：成功时退出码 0；失败时把原因打到 stderr 并退出非 0。
	Script string

	// Env 是额外的非敏感环境变量（主机、端口、库名、schema 等）。
	// ★ 密码不要写在这里，用 ValueFrom + SecretKeyRef。
	Env []corev1.EnvVar

	// RunAsUser / FSGroup 是预演容器要用的非 root uid。
	//
	// ★ 为什么由 driver 给而不是 controller 写死：
	//   不同镜像里那个非 root 用户的 uid 不一样
	//   （postgres:15-alpine 是 70，postgres:15 Debian 是 999，mysql 又是别的）。
	//   ★ 而且不能在 Pod 上写 RunAsNonRoot: true —— 官方 postgres 镜像
	//     没有 USER 指令（默认 root），kubelet 会直接拒绝启动。
	RunAsUser *int64
	FSGroup   *int64
}

// ShadowValidator 是 Persistent 的【可选】扩展。
//
// 实现了它 = 这个 driver 支持影子预演。
// 没实现 → controller 明确拒绝（不静默跳过 —— 静默跳过比报错更危险）。
//
// ★ 这样 controller 里不会再出现任何 driver 名字。
type ShadowValidator interface {
	Persistent

	// ShadowSpec 组装预演容器的规格。
	//
	// cfg 是本 driver 自己的 Config 实现（PostgresConfig / MysqlConfig…），
	// 实现方可以断言回具体类型拿需要的字段。
	ShadowSpec(cfg Config, applySQL []string) (ShadowSpec, error)
}
