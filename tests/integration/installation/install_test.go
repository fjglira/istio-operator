package installation

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	print("Command: " + kustomize + " edit set image controller=" + image)
	cmd := exec.Command(kustomize, "edit", "set", "image", fmt.Sprintf("controller=%s", image))
	dir, _ := getBaseDirectory()
	cmd.Dir = dir + "/config/manager"
	return cmd.Run()
}

func setNamespace() error {
	print("Setting Namespace\n")
	cmd := exec.Command(kustomize, "edit", "set", "namespace", namespace)
	cmd.Dir = wd + "/config/default"
	return cmd.Run()
}

func getBaseDirectory() (string, error) {
	// Get the absolute path of the currently running executable
	executablePath, err := os.Executable()
	if err != nil {
		return "", err
	}

	// Get the directory of the executable
	executableDir := filepath.Dir(executablePath)

	// Navigate up one directory to get the base directory of the project
	baseDir := filepath.Join(executableDir, "..")

	// Convert to absolute path
	absBaseDir, err := filepath.Abs(baseDir)
	if err != nil {
		return "", err
	}

	return absBaseDir, nil
}
