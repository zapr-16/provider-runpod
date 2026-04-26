package e2e_test

import (
	"context"
	"os"
	"testing"
	"time"

	runpodclient "github.com/zapr-16/provider-runpod/internal/clients"
)

const (
	runPodAPIKeyEnv    = "RUNPOD_API_KEY"
	testPortToken      = "8888/http"
	createPollInterval = 10 * time.Second
	createPollAttempts = 30
	deletePollInterval = 5 * time.Second
	deletePollAttempts = 12
)

func requireRunPodAPIKey(t *testing.T) string {
	t.Helper()

	apiKey := os.Getenv(runPodAPIKeyEnv)
	if apiKey == "" {
		t.Skip("RUNPOD_API_KEY not set")
	}

	return apiKey
}

func newRunPodClient(t *testing.T) *runpodclient.Client {
	t.Helper()
	return runpodclient.NewClient(requireRunPodAPIKey(t))
}

func TestPodLifecycle(t *testing.T) {
	client := newRunPodClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Minute)
	defer cancel()

	imageName := "python:3.11-slim"
	gpuCount := int32(1)
	cloudType := "SECURE"

	req := runpodclient.CreatePodRequest{
		ImageName:      &imageName,
		GPUCount:       &gpuCount,
		CloudType:      &cloudType,
		DockerStartCmd: []string{"python", "-m", "http.server", "8888"},
		Ports:          []string{testPortToken},
	}

	podID, err := client.CreatePod(ctx, req)
	if err != nil {
		t.Fatalf("CreatePod() error = %v", err)
	}
	if podID == "" {
		t.Fatal("CreatePod() returned empty pod ID")
	}

	deleted := false
	t.Cleanup(func() {
		if podID == "" || deleted {
			return
		}

		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()

		if err := client.DeletePod(cleanupCtx, podID); err != nil {
			t.Logf("cleanup DeletePod(%q) error: %v", podID, err)
		}
	})

	var response *runpodclient.PodResponse
	lastStatus := ""
	for i := 0; i < createPollAttempts; i++ {
		response, _, err = client.GetPod(ctx, podID)
		if err != nil {
			t.Fatalf("GetPod(%q) error = %v", podID, err)
		}

		if response != nil {
			lastStatus = response.DesiredStatus
			if response.PublicIP != "" && response.PortMappings != nil {
				break
			}
		}

		if i < createPollAttempts-1 {
			time.Sleep(createPollInterval)
		}
	}

	if response == nil || response.PublicIP == "" || response.PortMappings == nil {
		t.Fatalf("pod %s did not become network-ready within 5 minutes; last status: %s", podID, lastStatus)
	}

	if response.DesiredStatus != "RUNNING" {
		t.Fatalf("GetPod(%q) desiredStatus = %q, want %q", podID, response.DesiredStatus, "RUNNING")
	}
	if response.PublicIP == "" {
		t.Fatalf("GetPod(%q) publicIp is empty", podID)
	}

	externalPort, ok := response.PortMappings[testPortToken]
	if !ok {
		t.Fatalf("GetPod(%q) portMappings missing %q: %#v", podID, testPortToken, response.PortMappings)
	}
	if externalPort <= 0 {
		t.Fatalf("GetPod(%q) external port = %d, want positive integer", podID, externalPort)
	}

	if err := client.DeletePod(ctx, podID); err != nil {
		t.Fatalf("DeletePod(%q) error = %v", podID, err)
	}
	deleted = true

	for i := 0; i < deletePollAttempts; i++ {
		_, found, err := client.GetPod(ctx, podID)
		if err != nil {
			t.Fatalf("GetPod(%q) after delete error = %v", podID, err)
		}
		if !found {
			return
		}

		if i < deletePollAttempts-1 {
			time.Sleep(deletePollInterval)
		}
	}

	t.Fatalf("pod %s still exists 60 seconds after DeletePod()", podID)
}
