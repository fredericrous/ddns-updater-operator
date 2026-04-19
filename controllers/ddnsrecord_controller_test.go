package controllers

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	connectivityv1alpha1 "github.com/fredericrous/homelab/ddns-updater-operator/api/v1alpha1"
)

var _ = Describe("DDNSRecord controller", func() {
	const (
		timeout  = 10 * time.Second
		interval = 250 * time.Millisecond
	)

	newRecord := func(name, host string) *connectivityv1alpha1.DDNSRecord {
		return &connectivityv1alpha1.DDNSRecord{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: "default",
			},
			Spec: connectivityv1alpha1.DDNSRecordSpec{
				Provider:  "ovh",
				Domain:    "example.test",
				Host:      host,
				IPVersion: "ipv4",
				ProviderConfig: connectivityv1alpha1.OVHProviderConfig{
					Mode: "api",
					CredentialsRef: connectivityv1alpha1.SecretReference{
						Name:      "ovh-creds",
						Namespace: "default",
					},
				},
			},
		}
	}

	Context("status.observedGeneration", func() {
		It("is set to metadata.generation after the first reconcile", func() {
			rec := newRecord("obs-gen-initial", "www")
			Expect(k8sClient.Create(ctx, rec)).To(Succeed())

			key := types.NamespacedName{Name: rec.Name, Namespace: rec.Namespace}

			Eventually(func(g Gomega) {
				var got connectivityv1alpha1.DDNSRecord
				g.Expect(k8sClient.Get(ctx, key, &got)).To(Succeed())
				g.Expect(got.Status.Ready).To(BeTrue())
				g.Expect(got.Status.ObservedGeneration).To(Equal(got.Generation))
				g.Expect(got.Status.LastSyncedAt).NotTo(BeNil())
			}, timeout, interval).Should(Succeed())
		})

		It("tracks generation across spec updates", func() {
			rec := newRecord("obs-gen-update", "api")
			Expect(k8sClient.Create(ctx, rec)).To(Succeed())

			key := types.NamespacedName{Name: rec.Name, Namespace: rec.Namespace}

			Eventually(func(g Gomega) {
				var got connectivityv1alpha1.DDNSRecord
				g.Expect(k8sClient.Get(ctx, key, &got)).To(Succeed())
				g.Expect(got.Status.ObservedGeneration).To(Equal(got.Generation))
			}, timeout, interval).Should(Succeed())

			var current connectivityv1alpha1.DDNSRecord
			Expect(k8sClient.Get(ctx, key, &current)).To(Succeed())
			originalGen := current.Generation
			current.Spec.Host = "api-updated"
			Expect(k8sClient.Update(ctx, &current)).To(Succeed())

			Eventually(func(g Gomega) {
				var got connectivityv1alpha1.DDNSRecord
				g.Expect(k8sClient.Get(ctx, key, &got)).To(Succeed())
				g.Expect(got.Generation).To(BeNumerically(">", originalGen))
				g.Expect(got.Status.ObservedGeneration).To(Equal(got.Generation))
			}, timeout, interval).Should(Succeed())
		})
	})

	Context("status-write quiescence", func() {
		It("stops rewriting LastSyncedAt once the spec is fully observed", func() {
			// Regression guard for the reconcile loop: before the fix, every
			// reconcile wrote Status.LastSyncedAt = now, cascading watch
			// events forever. Now the write is guarded by
			// Ready && ObservedGeneration == Generation.
			rec := newRecord("quiet-after-convergence", "quiet")
			Expect(k8sClient.Create(ctx, rec)).To(Succeed())

			key := types.NamespacedName{Name: rec.Name, Namespace: rec.Namespace}

			var first *metav1.Time
			Eventually(func(g Gomega) {
				var got connectivityv1alpha1.DDNSRecord
				g.Expect(k8sClient.Get(ctx, key, &got)).To(Succeed())
				g.Expect(got.Status.ObservedGeneration).To(Equal(got.Generation))
				first = got.Status.LastSyncedAt
				g.Expect(first).NotTo(BeNil())
			}, timeout, interval).Should(Succeed())

			time.Sleep(2 * time.Second)

			var got connectivityv1alpha1.DDNSRecord
			Expect(k8sClient.Get(ctx, key, &got)).To(Succeed())
			Expect(got.Status.LastSyncedAt.Equal(first)).To(BeTrue(),
				"LastSyncedAt should not advance while spec is unchanged (got %v, was %v)",
				got.Status.LastSyncedAt, first)
		})
	})
})
