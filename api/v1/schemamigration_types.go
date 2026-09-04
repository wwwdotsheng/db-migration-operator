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
	Operation SchemaMigrationOperation `json:"operation"`
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
	// +kubebuilder:validation:MinItems=1
	// +optional
	RollbackSql []string `json:"rollbackSql,omitempty"`
}

type SchemaMigrationOperation string

const (
	SchemaMigrationOperationApply    = "apply"
	SchemaMigrationOperationRollback = "rollback"
)

type SchemaMigrationStatusPhase string

const (
	SchemaMigrationStatusPhaseApplying       SchemaMigrationStatusPhase = "Applying"
	SchemaMigrationStatusPhaseBlocked        SchemaMigrationStatusPhase = "Blocked"
	SchemaMigrationStatusPhaseFailed         SchemaMigrationStatusPhase = "Failed"
	SchemaMigrationStatusPhaseDone           SchemaMigrationStatusPhase = "Done"
	SchemaMigrationStatusPhaseRolling        SchemaMigrationStatusPhase = "Rolling"
	SchemaMigrationStatusPhaseRolledBack     SchemaMigrationStatusPhase = "RolledBack"
	SchemaMigrationStatusPhaseRollbackFailed SchemaMigrationStatusPhase = "RollbackFailed"
	SchemaMigrationStatusPhaseUnknown        SchemaMigrationStatusPhase = "Unknown"
)

var (
	SchemaMigrationStatusPhaseEnum = []SchemaMigrationStatusPhase{
		SchemaMigrationStatusPhaseApplying,
		SchemaMigrationStatusPhaseBlocked,
		SchemaMigrationStatusPhaseFailed,
		SchemaMigrationStatusPhaseDone,
		SchemaMigrationStatusPhaseRolling,
		SchemaMigrationStatusPhaseRolledBack,
		SchemaMigrationStatusPhaseRollbackFailed,
		SchemaMigrationStatusPhaseUnknown,
	}
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
