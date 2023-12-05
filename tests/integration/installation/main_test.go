package installation

import (
	"fmt"
	"os"
	"testing"
)

// Define common variables from the setup script to be used later if they are needed
var (
	command          = "kubectl"
	ocp              = getenv("OCP", "false")
	skipBuild        = getenv("SKIP_BUILD", "false")
	hub              = os.Getenv("HUB")
	image_base       = os.Getenv("IMAGE_BASE")
	image            = os.Getenv("IMAGE")
	tag              = os.Getenv("TAG")
	istio_manifest   = ""
	timeout          = "180s"
	namespace        = getenv("NAMESPACE", "istio-operator")
	deployment_name  = getenv("DEPLOYMENT_NAME", "istio-operator")
	control_plane_ns = getenv("CONTROL_PLANE_NS", "istio-system")
	wd               = os.Getenv("WD")
	deploy_operator  = getenv("DEPLOY_OPERATOR", "true")
	target           = "deploy"
)

func TestMain(t *testing.M) {
	// Print the current value of the variables defined above
	println("************ Variables ************")
	println("command: " + command)
	println("ocp: " + ocp)
	println("skipBuild: " + skipBuild)
	println("hub: " + hub)
	println("image_base: " + image_base)
	println("image: " + image)
	println("tag: " + tag)
	println("istio_manifest: " + istio_manifest)
	println("timeout: " + timeout)
	println("namespace: " + namespace)
	println("deployment_name: " + deployment_name)
	println("control_plane_ns: " + control_plane_ns)
	println("wd: " + wd)
	println("deploy_operator: " + deploy_operator)
	println("target: " + target)

	setup()
	// Run the tests
	exitCode := t.Run()

	// Teardown code here (if needed)
	teardown()

	// Exit with the same code as the test
	os.Exit(exitCode)
}

func setup() {
	println("************ Running Setup ************")
	if ocp == "true" {
		command = "oc"
		fmt.Printf("Absolute Path: %s\n", wd)
		istio_manifest = fmt.Sprintf(wd, "/config/samples/istio-sample-openshift.yaml")
		setupRunOCP()
	} else {
		istio_manifest = fmt.Sprintf(wd, "/config/samples/istio-sample-kubernetes.yaml")
		setupRunKind()
	}

}

func setupRunOCP() {
	// The run-integ-suite-ocp.sh runs the setup before running the test, for additional custom setup run the code here
}

func setupRunKind() {
	// The Kind provisioner located on common/scripts/kind_provisioner.sh setup the cluster before running the test
	// And the run-integ-suite-kind.sh rns additional setup before running the test

}

func teardown() {
	// Clean up resources after tests if needed
}

// getenv returns an environment variable value or the given fallback as a default value.
func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if len(value) == 0 {
		return fallback
	}
	return value
}
