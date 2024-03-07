// Copyright Istio Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR Condition OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package integrationoperator

import (
	"fmt"
	"time"

	. "github.com/istio-ecosystem/sail-operator/pkg/util/tests/ginkgo"
	"github.com/istio-ecosystem/sail-operator/pkg/util/tests/kubectl"
	resourcecondition "github.com/istio-ecosystem/sail-operator/pkg/util/tests/types"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Operator", Ordered, func() {
	SetDefaultEventuallyTimeout(120 * time.Second)
	SetDefaultEventuallyPollingInterval(time.Second)
	var (
		resourceAvailable = resourcecondition.Condition{
			Type:   "Available",
			Status: "True",
		}
		resourceReconcilied = resourcecondition.Condition{
			Type:   "Reconciled",
			Status: "True",
		}

		resourceReady = resourcecondition.Condition{
			Type:   "Ready",
			Status: "True",
		}

		istioResources = []string{
			// TODO: Find an alternative to this list
			"authorizationpolicies.security.istio.io",
			"destinationrules.networking.istio.io",
			"envoyfilters.networking.istio.io",
			"gateways.networking.istio.io",
			"istiorevisions.operator.istio.io",
			"istios.operator.istio.io",
			"peerauthentications.security.istio.io",
			"proxyconfigs.networking.istio.io",
			"requestauthentications.security.istio.io",
			"serviceentries.networking.istio.io",
			"sidecars.networking.istio.io",
			"telemetries.telemetry.istio.io",
			"virtualservices.networking.istio.io",
			"wasmplugins.extensions.istio.io",
			"workloadentries.networking.istio.io",
			"workloadgroups.networking.istio.io",
		}
	)

	Describe("installation", func() {
		// TODO: we  need to support testing both types of deployment for the operator, helm and olm via subscription.
		// Discuss with the team if we should add a flag to the test to enable the olm deployment and don't do that deployment in different step
		When("default helm manifest are applied", func() {
			BeforeAll(func() {
				if skipDeploy == "true" {
					Skip("Skipping the deployment of the operator and the tests")
				}
				Success("Deploying Operator using default helm charts located in /chart folder")
				Eventually(deployOperator).Should(Succeed(), "Operator failed to be deployed; unexpected error")
			})

			It("starts successfully", func() {
				Eventually(kubectl.GetResourceCondition).
					WithArguments(namespace, "deployment", deploymentName).
					Should(ContainElement(resourceAvailable))
				Success("Operator deployment is Available")

				Expect(kubectl.GetPodPhase(namespace, "control-plane=istio-operator")).Should(Equal("Running"), "Operator failed to start; unexpected pod Phase")
				Success("Istio-operator pod is Running")
			})

			It("deploys all the CRDs", func() {
				Eventually(kubectl.GetCRDs).
					Should(ContainElements(istioResources), "Istio CRDs are not present; expected list to contain all elements")
				Success("Istio CRDs are present")
			})
		})
	})

	Describe("installation and unistallation of the istio resource", func() {
		AfterAll(func() {
			// Cleanup the operator at the end of the test
			Eventually(undeployOperator).Should(Succeed(), "Operator failed to be deleted; unexpected error")
			Success("Operator deployment is deleted")
		})

		for _, version := range istioVersions {
			// Note: This var version is needed to avoid the closure of the loop
			version := version

			Context(fmt.Sprintf("version %s", version), func() {
				BeforeAll(func() {
					Expect(kubectl.CreateNamespace(controlPlaneNamespace)).To(Succeed(), "Namespace failed to be created; unexpected error")

					By("Creating Istio CR")
					Eventually(createIstioCR).
						WithArguments(version).
						Should(Succeed(), "Istio CR failed to be created; unexpected error")
					Success("Istio CR created")
				})

				AfterAll(func() {
					By("Cleaning up")
					Eventually(kubectl.DeleteNamespace).
						WithArguments(controlPlaneNamespace).
						Should(Succeed(), "Namespace failed to be deleted; unexpected error")

					Eventually(kubectl.CheckNamespaceExist).
						WithArguments(controlPlaneNamespace).
						Should(MatchError(kubectl.ErrNotFound), "Namespace should not exist; unexpected error")
					Success("Cleanup done")
				})

				When("the resource is created", func() {
					It("updates the Istio resource status to Reconcilied and Ready", func() {
						Eventually(kubectl.GetResourceCondition).
							WithArguments(controlPlaneNamespace, "istio", istioName).
							Should(ContainElement(resourceReconcilied), "Istio it's not Reconcilied; unexpected Condition")

						Eventually(kubectl.GetResourceCondition).
							WithArguments(controlPlaneNamespace, "istio", istioName).
							Should(ContainElement(resourceReady), "Istio it's not Ready; unexpected Condition")

						Eventually(kubectl.GetPodPhase).
							WithArguments(controlPlaneNamespace, "app=istiod").
							Should(Equal("Running"), "Istiod is not running; unexpected pod Phase")
						Success("Istio resource is Reconcilied and Ready")
					})

					It("deploys correct istiod image tag according to the version in the Istio CR", func() {
						// TODO: we need to add a function to get the istio version from the control panel directly
						// and compare it with the applied version
						// This is a TODO because actual version.yaml contains for example latest and not the actual version
						// Posible solution is to add actual version to the version.yaml
					})

					It("doesn't continuously reconcile the istio resource", func() {
						Eventually(kubectl.GetPodLogs).
							WithArguments(controlPlaneNamespace, "app=istiod", 30*time.Second).
							ShouldNot(ContainSubstring("Reconciliation done"), "Istio Operator is continuously reconciling")
						Success("Istio Operator stopped reconciling")
					})

					It("deploys istiod", func() {
						Expect(kubectl.GetDeploymentsNames(controlPlaneNamespace)).
							To(Equal([]string{"istiod"}), "Istiod deployment is not present; expected list to be equal")
						Success("Istiod deployment is present")
					})

					It("deploys the CNI DaemonSet when running on OpenShift", func() {
						if ocp == "true" {
							Eventually(kubectl.GetDaemonSets).
								WithArguments(namespace).
								Should(ContainElement("istio-cni-node"), "CNI DaemonSet is not deployed; expected list to contain element")

							Expect(kubectl.GetPodPhase(namespace, "k8s-app=istio-cni-node")).Should(Equal("Running"), "CNI Daemon is not running; unexpected Phase")
							Success("CNI DaemonSet is deployed in the namespace and Running")
						} else {
							Consistently(kubectl.GetDaemonSets).
								WithArguments(namespace).WithTimeout(30*time.Second).
								Should(BeEmpty(), "CNI DaemonSet it's present; expected list to be empty")
							Success("CNI DaemonSet is not deployed in the namespace because it's not OpenShift")
						}
					})
				})

				When("the Istio CR is deleted", func() {
					BeforeEach(func() {
						Expect(deleteIstioCR(version)).Should(Succeed(), "Istio CR deletion failed; unexpected error")
						Success("Istio CR deleted")
					})

					It("removes everything from the namespace", func() {
						Eventually(kubectl.GetAllResources).
							WithArguments(controlPlaneNamespace).
							Should(Equal(kubectl.EmptyResourceList), "Namespace should be empty")
						Success("Namespace is empty")
					})
				})
			})
		}
	})
})