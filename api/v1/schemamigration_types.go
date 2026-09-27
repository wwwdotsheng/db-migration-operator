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

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// SchemaMigrationSpec defines the desired state of SchemaMigration
type SchemaMigrationSpec struct {
	// +required
	Name string `json:"name"`
	// +required
	ChangeType SchemaMigrationChangeType `json:"changeType"`
	// +required
	Driver string `json:"driver"`
	// +kubebuilder:validation:XPreserveUnknownFields
	// +required
	DriverConfig runtime.RawExtension `json:"driverConfig"`
	// +kubebuilder:validation:Minimum=1
	// +required
	Version int `json:"version"`
	// +optional
	BeforeVersion *int `json:"beforeVersion,omitempty"`
	// +kubebuilder:validation:MinItems=1
	// +required
	ApplySql []string `json:"applySql"`
}

type SchemaMigrationChangeType string

const (
	ChangeTypeExpand   SchemaMigrationChangeType = "Expand"
	ChangeTypeContract SchemaMigrationChangeType = "Contract"
	ChangeTypeMixed    SchemaMigrationChangeType = "Mixed"
)

type SchemaMigrationStatusPhase string

const (
	PhaseApplying SchemaMigrationStatusPhase = "Applying"
	PhaseFailed   SchemaMigrationStatusPhase = "Failed"
	PhaseDone     SchemaMigrationStatusPhase = "Done"
	PhaseBlocked  SchemaMigrationStatusPhase = "Blocked"
	PhaseInit     SchemaMigrationStatusPhase = ""
)

// Condition 类型。用 status.conditions 表达「持续状态」——
// 跟 Event 的区别：Event 是瞬时记录会过期，Condition 是当前事实。
const (
	// ConditionShadowValidated 表示影子预演已经通过。
	// ★ 用 Condition 而不是 Phase 来判断「要不要预演」，
	//   因为 Phase 会被 Blocked 等中间态覆盖，而 Condition 是单调的。
	ConditionShadowValidated = "ShadowValidated"

	// ConditionBlocked 表示当前因为某个原因停在 Blocked。
	// Reason 区分原因：SecretNotFound / BeforeVersionNotSatisfied
	ConditionBlocked = "Blocked"
)

// Condition Reason 常量。
const (
	ReasonValidationPassed          = "ValidationPassed"
	ReasonValidationFailed          = "ValidationFailed"
	ReasonSecretNotFound            = "SecretNotFound"
	ReasonBeforeVersionNotSatisfied = "BeforeVersionNotSatisfied"
	ReasonMigrationApplied          = "MigrationApplied"
)

// SchemaMigrationStatus defines the observed state of SchemaMigration.
type SchemaMigrationStatus struct {
	// +optional
	Phase SchemaMigrationStatusPhase `json:"phase,omitempty"`
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// +optional
	Message string `json:"message,omitempty"`
	// +optional
	AppliedAt *metav1.Time `json:"appliedAt,omitempty"`
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// SchemaMigration is the Schema for the schemamigrations API
type SchemaMigration struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of SchemaMigration
	// +required
	Spec SchemaMigrationSpec `json:"spec"`

	// status defines the observed state of SchemaMigration
	// +optional
	Status SchemaMigrationStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// SchemaMigrationList contains a list of SchemaMigration
type SchemaMigrationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []SchemaMigration `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &SchemaMigration{}, &SchemaMigrationList{})
		return nil
	})
}
