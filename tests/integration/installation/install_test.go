package installation

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"testing"
)

const (
	localBin         = "bin"
	kustomizeCommand = "kustomize"
)

var (
	kustomize = fmt.Sprintf("%s/%s", localBin, kustomizeCommand)
)

func TestInstallOperator(t *testing.T) {
	print("Testing Installation of the Operator\n")
	deployOperator()
}

func TestUninstallOperator(t *testing.T) {

	print("Testing Uninstall of the Operator\n")
}

func deployOperator() {
	if ocp == "true" {
		deployOpenShift()
	} else {
		deployKubernetes()
	}
}

func deployKubernetes() error {
	// Generate deployment manifests
	cmd := exec.Command("kustomize", "build", "config/default")
	output, err := cmd.Output()
	print("******** Output ********\n")
	print(output)
	if err != nil {
		return err
	}

	// Apply deployment manifests to Kubernetes cluster
	cmd = exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = bytes.NewBuffer(output)
	if err := cmd.Run(); err != nil {
		return err
	}

	return nil
}

func deployOpenShift() error {

	// Ensure the local bin directory exists
	if _, err := os.Stat(localBin); os.IsNotExist(err) {
		if err := os.Mkdir(localBin, 0755); err != nil {
			fmt.Printf("Error creating local bin directory: %v\n", err)
			os.Exit(1)
		}
	}

	if err := setControllerImage(); err != nil {
		fmt.Printf("Error setting controller image: %v\n", err)
		os.Exit(1)
	}

	if err := setNamespace(); err != nil {
		fmt.Printf("Error setting namespace: %v\n", err)
		os.Exit(1)
	}

	output, err := exec.Command(kustomize, "build", "config/openshift").CombinedOutput()
	if err != nil {
		fmt.Printf("Error generating YAML manifests: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(output))

	return nil
}

func setControllerImage() error {
	print("Setting Controller Image\n")
	cmd := exec.Command(kustomize, "edit", "set", "image", fmt.Sprintf("controller=%s", image))
	cmd.Dir = "/home/fedora/repos/istio-operator/config/manager"
	print("********** Command **********\n")
	print(cmd)
	return cmd.Run()
}

func setNamespace() error {
	print("Setting Namespace\n")
	cmd := exec.Command(kustomize, "edit", "set", "namespace", namespace)
	cmd.Dir = wd + "/home/fedora/repos/istio-operator/config/default"
	print("********** Command **********\n")
	print(cmd)
	return cmd.Run()
}
