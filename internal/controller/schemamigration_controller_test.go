/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	dbv1 "github.com/wwwdotsheng/db-migration-operator/api/v1"
)

// validSpec 返回一份能通过 CRD 校验的 spec。
// 这些用例都不需要真库：非法 changeType 和终态都会在连库之前返回。
func validSpec(changeType dbv1.SchemaMigrationChangeType) dbv1.SchemaMigrationSpec {
	return dbv1.SchemaMigrationSpec{
		Name:       "test-migration",
		ChangeType: changeType,
		Driver:     "mysql",
		DriverConfig: runtime.RawExtension{
			Raw: []byte(`{"host":"mysql.example.com","port":3306,"user":"root","database":"testdb"}`),
		},
		Version:  1,
		ApplySql: []string{"alter table test_data add `remark` varchar(100) default null"},
	}
}

func newReconciler() *SchemaMigrationReconciler {
	return &SchemaMigrationReconciler{
		Client:   k8sClient,
		Scheme:   k8sClient.Scheme(),
		Recorder: record.NewFakeRecorder(10),
	}
}

var _ = Describe("SchemaMigration Controller", func() {
	ctx := context.Background()

	Context("当 CR 的 changeType 不受支持时", func() {
		const (
			resourceName      = "test-invalid-changetype"
			resourceNamespace = "default"
		)
		key := types.NamespacedName{Name: resourceName, Namespace: resourceNamespace}

		BeforeEach(func() {
			resource := &dbv1.SchemaMigration{
				ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: resourceNamespace},
				Spec:       validSpec(dbv1.SchemaMigrationChangeType("NotAChangeType")),
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())
		})

		AfterEach(func() {
			resource := &dbv1.SchemaMigration{}
			if err := k8sClient.Get(ctx, key, resource); err == nil {
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
			}
		})

		It("应该把 CR 标成 Failed 并返回 nil，不碰数据库", func() {
			recorder := record.NewFakeRecorder(10)
			r := newReconciler()
			r.Recorder = recorder

			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())

			var got dbv1.SchemaMigration
			Expect(k8sClient.Get(ctx, key, &got)).To(Succeed())
			Expect(got.Status.Phase).To(Equal(dbv1.PhaseFailed))
			Expect(got.Status.Message).To(ContainSubstring("unknown ChangeType"))

			Eventually(recorder.Events).Should(Receive(ContainSubstring("InvalidChangeType")))
		})
	})

	Context("当 CR 已处于终态时", func() {
		const (
			resourceName      = "test-terminal-phase"
			resourceNamespace = "default"
		)
		key := types.NamespacedName{Name: resourceName, Namespace: resourceNamespace}

		BeforeEach(func() {
			resource := &dbv1.SchemaMigration{
				ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: resourceNamespace},
				Spec:       validSpec(dbv1.ChangeTypeExpand),
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			resource.Status.Phase = dbv1.PhaseDone
			resource.Status.Message = "migration done"
			Expect(k8sClient.Status().Update(ctx, resource)).To(Succeed())
		})

		AfterEach(func() {
			resource := &dbv1.SchemaMigration{}
			if err := k8sClient.Get(ctx, key, resource); err == nil {
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
			}
		})

		It("应该直接返回且不改动 status", func() {
			r := newReconciler()

			result, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			var got dbv1.SchemaMigration
			Expect(k8sClient.Get(ctx, key, &got)).To(Succeed())
			Expect(got.Status.Phase).To(Equal(dbv1.PhaseDone))
			Expect(got.Status.Message).To(Equal("migration done"))
		})
	})

	Context("当资源不存在时", func() {
		It("应该忽略 NotFound 而不报错", func() {
			r := newReconciler()
			_, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "not-exist", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})
})

var _ = Describe("SchemaMigration CRD 校验", func() {
	It("应该在 version 小于 1 时拒绝创建", func() {
		spec := validSpec(dbv1.ChangeTypeExpand)
		spec.Version = 0
		resource := &dbv1.SchemaMigration{
			ObjectMeta: metav1.ObjectMeta{Name: "test-invalid-version", Namespace: "default"},
			Spec:       spec,
		}
		err := k8sClient.Create(context.Background(), resource)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsInvalid(err)).To(BeTrue())
	})

	It("应该在 applySql 为空时拒绝创建", func() {
		spec := validSpec(dbv1.ChangeTypeExpand)
		spec.ApplySql = nil
		resource := &dbv1.SchemaMigration{
			ObjectMeta: metav1.ObjectMeta{Name: "test-empty-applysql", Namespace: "default"},
			Spec:       spec,
		}
		err := k8sClient.Create(context.Background(), resource)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsInvalid(err)).To(BeTrue())
	})
})
