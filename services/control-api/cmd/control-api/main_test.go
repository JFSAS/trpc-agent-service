package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/bootstrap"
)

func TestPrintDeploymentContractDigestRequiresNoDatabaseOrKey(t *testing.T) {
	t.Setenv("CONTROL_DATABASE_URL", "")
	t.Setenv("CONTROL_PROFILE_CREDENTIAL_KEY", "")
	t.Setenv("CONTROL_DEPLOYMENT_EXPECTED_CONTRACT_DIGEST", "")
	t.Setenv("CONTROL_BOOTSTRAP_MODE", "invalid")
	t.Setenv("CONTROL_DEPLOYMENT_ALLOWED_ENDPOINT_HOSTS", "model.example,state.example")
	want, err := bootstrap.DeploymentContractDigestFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := runCommand([]string{"-print-deployment-contract-digest"}, &output); err != nil {
		t.Fatal(err)
	}
	if output.String() != want+"\n" {
		t.Fatalf("output = %q, want digest-only %q", output.String(), want+"\n")
	}
}

func TestPrintDeploymentContractDigestRejectsInvalidHosts(t *testing.T) {
	t.Setenv("CONTROL_DEPLOYMENT_ALLOWED_ENDPOINT_HOSTS", "UPPER.example")
	var output bytes.Buffer
	err := runCommand([]string{"-print-deployment-contract-digest"}, &output)
	if err == nil || !strings.Contains(err.Error(), "validate deployment platform contract") || output.Len() != 0 {
		t.Fatalf("output = %q, error = %v, want failed validation without a digest", output.String(), err)
	}
}

func TestEnvironmentExamplePinsMatchingDeploymentContract(t *testing.T) {
	data, err := os.ReadFile("../../../../.env.example")
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			values[key] = value
		}
	}
	hosts, exists := values["CONTROL_DEPLOYMENT_ALLOWED_ENDPOINT_HOSTS"]
	if !exists || hosts == "" {
		t.Fatal("environment example must fix its host configuration")
	}
	t.Setenv("CONTROL_DEPLOYMENT_ALLOWED_ENDPOINT_HOSTS", hosts)
	want, err := bootstrap.DeploymentContractDigestFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	if values["CONTROL_DEPLOYMENT_EXPECTED_CONTRACT_DIGEST"] != want {
		t.Fatal("environment example digest does not match its hosts and current frozen contract")
	}
}
