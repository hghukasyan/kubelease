/*
Copyright 2026 KubeLease Authors.

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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
	"github.com/hghukasyan/kubelease/internal/lease"
	"github.com/hghukasyan/kubelease/internal/remote"
)

func kubeconfigBytesForEnvtest() []byte {
	apiCfg := clientcmdapi.NewConfig()
	apiCfg.Clusters["envtest"] = &clientcmdapi.Cluster{
		Server:                   cfg.Host,
		CertificateAuthorityData: cfg.CAData,
		InsecureSkipTLSVerify:    cfg.CAData == nil,
	}
	apiCfg.AuthInfos["envtest"] = &clientcmdapi.AuthInfo{
		ClientCertificateData: cfg.CertData,
		ClientKeyData:         cfg.KeyData,
		Token:                 cfg.BearerToken,
	}
	apiCfg.Contexts["envtest"] = &clientcmdapi.Context{
		Cluster:  "envtest",
		AuthInfo: "envtest",
	}
	apiCfg.CurrentContext = "envtest"
	out, err := clientcmd.Write(*apiCfg)
	Expect(err).NotTo(HaveOccurred())
	return out
}

var _ = Describe("Multi-cluster EnvironmentLease", func() {
	var (
		ctx        context.Context
		reconciler *EnvironmentLeaseReconciler
		provider   remote.Provider
		clockTime  time.Time
	)

	BeforeEach(func() {
		ctx = context.Background()
		clockTime = time.Date(2025, 7, 4, 12, 0, 0, 0, time.UTC)
		provider = remote.NewProvider(remote.Options{
			Hub:    k8sClient,
			Scheme: k8sClient.Scheme(),
		})
		reconciler = &EnvironmentLeaseReconciler{
			Client:   k8sClient,
			Scheme:   k8sClient.Scheme(),
			Recorder: record.NewFakeRecorder(64),
			Clock:    lease.FixedClock{T: clockTime},
			Provider: provider,
		}
	})

	It("provisions Namespace via ClusterTarget using kubeconfig (same API as remote)", func() {
		secretNS := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kubelease-creds"}}
		Expect(k8sClient.Create(ctx, secretNS)).To(Succeed())

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "dev-east-kubeconfig", Namespace: "kubelease-creds"},
			Data:       map[string][]byte{"kubeconfig": kubeconfigBytesForEnvtest()},
		}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())

		target := &platformv1alpha1.ClusterTarget{
			ObjectMeta: metav1.ObjectMeta{Name: "dev-east"},
			Spec: platformv1alpha1.ClusterTargetSpec{
				Credentials: platformv1alpha1.ClusterCredentials{
					SecretRef: platformv1alpha1.SecretKeySelector{
						Name: "dev-east-kubeconfig", Namespace: "kubelease-creds", Key: "kubeconfig",
					},
				},
				Labels: map[string]string{"region": "us-east"},
			},
		}
		Expect(k8sClient.Create(ctx, target)).To(Succeed())

		leaseObj := &platformv1alpha1.EnvironmentLease{
			ObjectMeta: metav1.ObjectMeta{Name: "payment-pr-mc"},
			Spec: platformv1alpha1.EnvironmentLeaseSpec{
				ClusterRef: &platformv1alpha1.LocalObjectReference{Name: "dev-east"},
				TTL:        &metav1.Duration{Duration: 8 * time.Hour},
				MaxTTL:     &metav1.Duration{Duration: 24 * time.Hour},
				Renewable:  ptr.To(true),
				Namespace: platformv1alpha1.NamespaceSpec{
					GenerateName: "preview-",
					Labels:       map[string]string{"app": "payments"},
				},
				NetworkPolicy: &platformv1alpha1.NetworkPolicySpec{DefaultDeny: true},
			},
		}
		Expect(k8sClient.Create(ctx, leaseObj)).To(Succeed())

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: leaseObj.Name}})
		Expect(err).NotTo(HaveOccurred())
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: leaseObj.Name}})
		Expect(err).NotTo(HaveOccurred())

		updated := &platformv1alpha1.EnvironmentLease{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: leaseObj.Name}, updated)).To(Succeed())
		Expect(updated.Status.Namespace).NotTo(BeEmpty())
		Expect(updated.Status.Cluster).NotTo(BeNil())
		Expect(updated.Status.Cluster.Name).To(Equal("dev-east"))
		Expect(updated.Status.Phase).To(Equal(platformv1alpha1.LeasePhaseActive))

		ns := &corev1.Namespace{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: updated.Status.Namespace}, ns)).To(Succeed())
		Expect(ns.Labels[platformv1alpha1.LabelLease]).To(Equal(leaseObj.Name))
	})

	It("marks TargetClusterUnavailable for missing ClusterTarget", func() {
		leaseObj := &platformv1alpha1.EnvironmentLease{
			ObjectMeta: metav1.ObjectMeta{Name: "missing-target-lease"},
			Spec: platformv1alpha1.EnvironmentLeaseSpec{
				ClusterRef: &platformv1alpha1.LocalObjectReference{Name: "does-not-exist"},
				TTL:        &metav1.Duration{Duration: 1 * time.Hour},
				Namespace:  platformv1alpha1.NamespaceSpec{GenerateName: "preview-"},
			},
		}
		Expect(k8sClient.Create(ctx, leaseObj)).To(Succeed())

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: leaseObj.Name}})
		Expect(err).NotTo(HaveOccurred())
		res, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: leaseObj.Name}})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(BeNumerically(">", 0))

		updated := &platformv1alpha1.EnvironmentLease{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: leaseObj.Name}, updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(platformv1alpha1.LeasePhaseFailed))
	})

	It("falls back to local cluster when clusterRef is omitted", func() {
		leaseObj := &platformv1alpha1.EnvironmentLease{
			ObjectMeta: metav1.ObjectMeta{Name: "local-fallback-lease"},
			Spec: platformv1alpha1.EnvironmentLeaseSpec{
				TTL:       &metav1.Duration{Duration: 1 * time.Hour},
				Namespace: platformv1alpha1.NamespaceSpec{GenerateName: "local-"},
			},
		}
		Expect(k8sClient.Create(ctx, leaseObj)).To(Succeed())
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: leaseObj.Name}})
		Expect(err).NotTo(HaveOccurred())
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: leaseObj.Name}})
		Expect(err).NotTo(HaveOccurred())

		updated := &platformv1alpha1.EnvironmentLease{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: leaseObj.Name}, updated)).To(Succeed())
		Expect(updated.Status.Cluster).NotTo(BeNil())
		Expect(updated.Status.Cluster.Name).To(Equal(platformv1alpha1.LocalClusterName))
		Expect(updated.Status.Phase).To(Equal(platformv1alpha1.LeasePhaseActive))
	})
})
