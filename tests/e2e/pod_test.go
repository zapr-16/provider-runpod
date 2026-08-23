package e2e_test

import (
	"context"
	"fmt"
	"net/http"
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

	// HTTP ports are served through RunPod's TLS proxy at
	// https://{podID}-{port}.proxy.runpod.net. SECURE pods without
	// supportPublicIp may never receive a public IP, so proxy
	// reachability — not publicIp — is the readiness signal, matching
	// the controller's own probe semantics.
	proxyURL := fmt.Sprintf("https://%s-8888.proxy.runpod.net", podID)
	var response *runpodclient.PodResponse
	lastStatus := ""
	proxyReady := false
	for i := 0; i < createPollAttempts; i++ {
		response, _, err = client.GetPod(ctx, podID)
		if err != nil {
			t.Fatalf("GetPod(%q) error = %v", podID, err)
		}

		if response != nil {
			lastStatus = response.DesiredStatus
			if response.DesiredStatus == "RUNNING" && probeProxy(ctx, proxyURL) {
				proxyReady = true
				break
			}
		}

		if i < createPollAttempts-1 {
			time.Sleep(createPollInterval)
		}
	}

	if !proxyReady {
		t.Fatalf("pod %s did not become reachable via %s within 5 minutes; last status: %s", podID, proxyURL, lastStatus)
	}

	if response.DesiredStatus != "RUNNING" {
		t.Fatalf("GetPod(%q) desiredStatus = %q, want %q", podID, response.DesiredStatus, "RUNNING")
	}

	// Public IP and port mappings are only assigned on some machine
	// configurations; assert their shape only when present.
	if response.PublicIP != "" {
		externalPort, ok := response.PortMappings[testPortToken]
		if !ok {
			t.Fatalf("GetPod(%q) portMappings missing %q: %#v", podID, testPortToken, response.PortMappings)
		}
		if externalPort <= 0 {
			t.Fatalf("GetPod(%q) external port = %d, want positive integer", podID, externalPort)
		}
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

var proxyProbeClient = &http.Client{Timeout: 10 * time.Second}

// probeProxy reports whether the RunPod proxy answers for the pod: any
// status below 500 proves the container is listening, while 502/503/504
// come from the proxy itself until the workload is up.
func probeProxy(ctx context.Context, url string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := proxyProbeClient.Do(req)
	if err != nil {
		return false
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	return resp.StatusCode < http.StatusInternalServerError
}
