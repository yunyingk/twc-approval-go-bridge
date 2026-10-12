package version

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
)

// TestVersionConsistency ensures that all build, deploy, and manifest files
// declare the exact same semantic release version as internal/version.Version.
func TestVersionConsistency(t *testing.T) {
	_, b, _, _ := runtime.Caller(0)
	repoRoot := filepath.Join(filepath.Dir(b), "../..")

	targetVersion := Version
	if targetVersion == "" {
		t.Fatal("version.Version must not be empty")
	}

	// 1. Check Makefile (echo X.Y.Z)
	makefileBytes, err := os.ReadFile(filepath.Join(repoRoot, "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	makeRe := regexp.MustCompile(`VERSION \?=.*echo\s+([0-9]+\.[0-9]+\.[0-9]+)`)
	makeMatch := makeRe.FindStringSubmatch(string(makefileBytes))
	if len(makeMatch) < 2 {
		t.Fatal("failed to extract fallback VERSION from Makefile")
	}
	if makeMatch[1] != targetVersion {
		t.Errorf("Makefile version drift: expected %s, got %s", targetVersion, makeMatch[1])
	}

	// 2. Check deploy/Dockerfile (ARG VERSION=X.Y.Z)
	dockerfileBytes, err := os.ReadFile(filepath.Join(repoRoot, "deploy/Dockerfile"))
	if err != nil {
		t.Fatalf("read deploy/Dockerfile: %v", err)
	}
	dockerRe := regexp.MustCompile(`ARG\s+VERSION=([0-9]+\.[0-9]+\.[0-9]+)`)
	dockerMatch := dockerRe.FindStringSubmatch(string(dockerfileBytes))
	if len(dockerMatch) < 2 {
		t.Fatal("failed to extract ARG VERSION from deploy/Dockerfile")
	}
	if dockerMatch[1] != targetVersion {
		t.Errorf("deploy/Dockerfile version drift: expected %s, got %s", targetVersion, dockerMatch[1])
	}

	// 3. Check deploy/k8s/twc-approval.yaml (image: ...:vX.Y.Z)
	k8sBytes, err := os.ReadFile(filepath.Join(repoRoot, "deploy/k8s/twc-approval.yaml"))
	if err == nil {
		k8sRe := regexp.MustCompile(`twc-approval-go-bridge:v?([0-9]+\.[0-9]+\.[0-9]+)`)
		k8sMatch := k8sRe.FindStringSubmatch(string(k8sBytes))
		if len(k8sMatch) >= 2 && k8sMatch[1] != targetVersion {
			t.Errorf("deploy/k8s/twc-approval.yaml image tag drift: expected %s, got %s", targetVersion, k8sMatch[1])
		}
	}
}
