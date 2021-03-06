/*
 * This file is part of the KubeVirt project
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * Copyright 2018 Red Hat, Inc.
 *
 */

package admitters

import (
	"encoding/json"

	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"k8s.io/api/admission/v1beta1"
	corev1 "k8s.io/api/core/v1"
	k8sv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	v1 "kubevirt.io/client-go/api/v1"
	snapshotv1 "kubevirt.io/client-go/apis/snapshot/v1alpha1"
	"kubevirt.io/client-go/kubecli"
	"kubevirt.io/kubevirt/pkg/testutils"
	"kubevirt.io/kubevirt/pkg/virt-api/webhooks"
	virtconfig "kubevirt.io/kubevirt/pkg/virt-config"
)

var _ = Describe("Validating VirtualMachineSnapshot Admitter", func() {
	vmName := "vm"
	apiGroup := "kubevirt.io/v1alpha3"

	config, configMapInformer, _, _ := testutils.NewFakeClusterConfig(&k8sv1.ConfigMap{})

	Context("Without feature gate enabled", func() {
		It("should reject anything", func() {
			snapshot := &snapshotv1.VirtualMachineSnapshot{
				Spec: snapshotv1.VirtualMachineSnapshotSpec{},
			}

			ar := createAdmissionReview(snapshot)
			resp := createTestVMSnapshotAdmitter(config, nil).Admit(ar)
			Expect(resp.Allowed).To(BeFalse())
			Expect(resp.Result.Message).Should(Equal("Snapshot feature gate not enabled"))
		})
	})

	Context("With feature gate enabled", func() {
		enableFeatureGate := func(featureGate string) {
			testutils.UpdateFakeClusterConfig(configMapInformer, &k8sv1.ConfigMap{
				Data: map[string]string{virtconfig.FeatureGatesKey: featureGate},
			})
		}

		disableFeatureGates := func() {
			testutils.UpdateFakeClusterConfig(configMapInformer, &k8sv1.ConfigMap{})
		}

		BeforeEach(func() {
			enableFeatureGate("Snapshot")
		})

		AfterEach(func() {
			disableFeatureGates()
		})

		It("should reject invalid request resource", func() {
			ar := &v1beta1.AdmissionReview{
				Request: &v1beta1.AdmissionRequest{
					Resource: webhooks.VirtualMachineGroupVersionResource,
				},
			}

			resp := createTestVMSnapshotAdmitter(config, nil).Admit(ar)
			Expect(resp.Allowed).To(BeFalse())
			Expect(resp.Result.Message).Should(ContainSubstring("Unexpected Resource"))
		})

		It("should reject missing apigroup", func() {
			snapshot := &snapshotv1.VirtualMachineSnapshot{
				Spec: snapshotv1.VirtualMachineSnapshotSpec{},
			}

			ar := createAdmissionReview(snapshot)
			resp := createTestVMSnapshotAdmitter(config, nil).Admit(ar)
			Expect(resp.Allowed).To(BeFalse())
			Expect(len(resp.Result.Details.Causes)).To(Equal(1))
			Expect(resp.Result.Details.Causes[0].Field).To(Equal("spec.source.apiGroup"))
		})

		It("should reject when VM does not exist", func() {
			snapshot := &snapshotv1.VirtualMachineSnapshot{
				Spec: snapshotv1.VirtualMachineSnapshotSpec{
					Source: corev1.TypedLocalObjectReference{
						APIGroup: &apiGroup,
						Kind:     "VirtualMachine",
						Name:     vmName,
					},
				},
			}

			ar := createAdmissionReview(snapshot)
			resp := createTestVMSnapshotAdmitter(config, nil).Admit(ar)
			Expect(resp.Allowed).To(BeFalse())
			Expect(len(resp.Result.Details.Causes)).To(Equal(1))
			Expect(resp.Result.Details.Causes[0].Field).To(Equal("spec.source.name"))
		})

		It("should reject spec update", func() {
			snapshot := &snapshotv1.VirtualMachineSnapshot{
				Spec: snapshotv1.VirtualMachineSnapshotSpec{
					Source: corev1.TypedLocalObjectReference{
						APIGroup: &apiGroup,
						Kind:     "VirtualMachine",
						Name:     vmName,
					},
				},
			}

			oldSnapshot := &snapshotv1.VirtualMachineSnapshot{
				Spec: snapshotv1.VirtualMachineSnapshotSpec{
					Source: corev1.TypedLocalObjectReference{
						APIGroup: &apiGroup,
						Kind:     "VirtualMachine",
						Name:     "baz",
					},
				},
			}

			ar := createUpdateAdmissionReview(oldSnapshot, snapshot)
			resp := createTestVMSnapshotAdmitter(config, nil).Admit(ar)
			Expect(resp.Allowed).To(BeFalse())
			Expect(len(resp.Result.Details.Causes)).To(Equal(1))
			Expect(resp.Result.Details.Causes[0].Field).To(Equal("spec"))
		})

		It("should allow metadata update", func() {
			oldSnapshot := &snapshotv1.VirtualMachineSnapshot{
				Spec: snapshotv1.VirtualMachineSnapshotSpec{
					Source: corev1.TypedLocalObjectReference{
						APIGroup: &apiGroup,
						Kind:     "VirtualMachine",
						Name:     vmName,
					},
				},
			}

			snapshot := &snapshotv1.VirtualMachineSnapshot{
				ObjectMeta: metav1.ObjectMeta{
					Finalizers: []string{"finalizer"},
				},
				Spec: snapshotv1.VirtualMachineSnapshotSpec{
					Source: corev1.TypedLocalObjectReference{
						APIGroup: &apiGroup,
						Kind:     "VirtualMachine",
						Name:     vmName,
					},
				},
			}

			ar := createUpdateAdmissionReview(oldSnapshot, snapshot)
			resp := createTestVMSnapshotAdmitter(config, nil).Admit(ar)
			Expect(resp.Allowed).To(BeTrue())
		})

		Context("when VirtualMachine exists", func() {
			var vm *v1.VirtualMachine

			BeforeEach(func() {
				vm = &v1.VirtualMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name: vmName,
					},
				}
			})

			It("should reject when VM is running", func() {
				snapshot := &snapshotv1.VirtualMachineSnapshot{
					Spec: snapshotv1.VirtualMachineSnapshotSpec{
						Source: corev1.TypedLocalObjectReference{
							APIGroup: &apiGroup,
							Kind:     "VirtualMachine",
							Name:     vmName,
						},
					},
				}

				t := true
				vm.Spec.Running = &t

				ar := createAdmissionReview(snapshot)
				resp := createTestVMSnapshotAdmitter(config, vm).Admit(ar)
				Expect(resp.Allowed).To(BeFalse())
				Expect(len(resp.Result.Details.Causes)).To(Equal(1))
				Expect(resp.Result.Details.Causes[0].Field).To(Equal("spec.source.name"))
			})

			It("should reject invalid kind", func() {
				snapshot := &snapshotv1.VirtualMachineSnapshot{
					Spec: snapshotv1.VirtualMachineSnapshotSpec{
						Source: corev1.TypedLocalObjectReference{
							APIGroup: &apiGroup,
							Kind:     "VirtualMachineInstance",
							Name:     vmName,
						},
					},
				}

				t := true
				vm.Spec.Running = &t

				ar := createAdmissionReview(snapshot)
				resp := createTestVMSnapshotAdmitter(config, vm).Admit(ar)
				Expect(resp.Allowed).To(BeFalse())
				Expect(len(resp.Result.Details.Causes)).To(Equal(1))
				Expect(resp.Result.Details.Causes[0].Field).To(Equal("spec.source.kind"))
			})

			It("should reject invalid apiGroup", func() {
				g := "foo.bar"
				snapshot := &snapshotv1.VirtualMachineSnapshot{
					Spec: snapshotv1.VirtualMachineSnapshotSpec{
						Source: corev1.TypedLocalObjectReference{
							APIGroup: &g,
							Kind:     "VirtualMachine",
							Name:     vmName,
						},
					},
				}

				t := true
				vm.Spec.Running = &t

				ar := createAdmissionReview(snapshot)
				resp := createTestVMSnapshotAdmitter(config, vm).Admit(ar)
				Expect(resp.Allowed).To(BeFalse())
				Expect(len(resp.Result.Details.Causes)).To(Equal(1))
				Expect(resp.Result.Details.Causes[0].Field).To(Equal("spec.source.apiGroup"))
			})

			It("should accept when VM is not running", func() {
				snapshot := &snapshotv1.VirtualMachineSnapshot{
					Spec: snapshotv1.VirtualMachineSnapshotSpec{
						Source: corev1.TypedLocalObjectReference{
							APIGroup: &apiGroup,
							Kind:     "VirtualMachine",
							Name:     vmName,
						},
					},
				}

				f := false
				vm.Spec.Running = &f

				ar := createAdmissionReview(snapshot)
				resp := createTestVMSnapshotAdmitter(config, vm).Admit(ar)
				Expect(resp.Allowed).To(BeTrue())
			})
		})
	})
})

func createAdmissionReview(snapshot *snapshotv1.VirtualMachineSnapshot) *v1beta1.AdmissionReview {
	bytes, _ := json.Marshal(snapshot)

	ar := &v1beta1.AdmissionReview{
		Request: &v1beta1.AdmissionRequest{
			Operation: v1beta1.Create,
			Namespace: "foo",
			Resource: metav1.GroupVersionResource{
				Group:    "snapshot.kubevirt.io",
				Resource: "virtualmachinesnapshots",
			},
			Object: runtime.RawExtension{
				Raw: bytes,
			},
		},
	}

	return ar
}

func createUpdateAdmissionReview(old, current *snapshotv1.VirtualMachineSnapshot) *v1beta1.AdmissionReview {
	oldBytes, _ := json.Marshal(old)
	currentBytes, _ := json.Marshal(current)

	ar := &v1beta1.AdmissionReview{
		Request: &v1beta1.AdmissionRequest{
			Operation: v1beta1.Update,
			Namespace: "foo",
			Resource: metav1.GroupVersionResource{
				Group:    "snapshot.kubevirt.io",
				Resource: "virtualmachinesnapshots",
			},
			Object: runtime.RawExtension{
				Raw: currentBytes,
			},
			OldObject: runtime.RawExtension{
				Raw: oldBytes,
			},
		},
	}

	return ar
}

func createTestVMSnapshotAdmitter(config *virtconfig.ClusterConfig, vm *v1.VirtualMachine) *VMSnapshotAdmitter {
	ctrl := gomock.NewController(GinkgoT())
	virtClient := kubecli.NewMockKubevirtClient(ctrl)
	vmInterface := kubecli.NewMockVirtualMachineInterface(ctrl)
	virtClient.EXPECT().VirtualMachine(gomock.Any()).Return(vmInterface).AnyTimes()
	if vm == nil {
		err := errors.NewNotFound(schema.GroupResource{Group: "kubevirt.io", Resource: "virtualmachines"}, "foo")
		vmInterface.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, err)
	} else {
		vmInterface.EXPECT().Get(vm.Name, gomock.Any()).Return(vm, nil)
	}
	return &VMSnapshotAdmitter{Config: config, Client: virtClient}
}
