package e2e_test

import (
	"context"
	"testing"
	"time"

	runpodclient "github.com/zapr-16/provider-runpod/internal/clients"
)

// TestEndpointLifecycle exercises the serverless template+endpoint flow the
// Endpoint controller performs: create template → create endpoint → get →
// patch → delete endpoint → delete template. With workersMin=0 the endpoint
// never rents a GPU, so the whole cycle is free and takes seconds.
func TestEndpointLifecycle(t *testing.T) {
	client := newRunPodClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	containerDisk := int32(10)
	templateID, err := client.CreateTemplate(ctx, runpodclient.CreateTemplateRequest{
		Name:              "provider-e2e-endpoint",
		ImageName:         "runpod/mock-worker:dev",
		IsServerless:      true,
		Env:               map[string]string{"MOCK": "true"},
		ContainerDiskInGb: &containerDisk,
	})
	if err != nil {
		t.Fatalf("CreateTemplate() error = %v", err)
	}
	if templateID == "" {
		t.Fatal("CreateTemplate() returned empty template ID")
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		if err := client.DeleteTemplate(cleanupCtx, templateID); err != nil {
			t.Logf("cleanup DeleteTemplate(%q) error: %v", templateID, err)
		}
	})

	name := "provider-e2e-endpoint"
	workersMin := int32(0)
	workersMax := int32(1)
	endpointID, err := client.CreateEndpoint(ctx, runpodclient.CreateEndpointRequest{
		Name:       &name,
		TemplateID: templateID,
		GPUTypeIDs: []string{"NVIDIA GeForce RTX 3090", "NVIDIA GeForce RTX 4090"},
		WorkersMin: &workersMin,
		WorkersMax: &workersMax,
	})
	if err != nil {
		t.Fatalf("CreateEndpoint() error = %v", err)
	}
	if endpointID == "" {
		t.Fatal("CreateEndpoint() returned empty endpoint ID")
	}

	deleted := false
	t.Cleanup(func() {
		if deleted {
			return
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		if err := client.DeleteEndpoint(cleanupCtx, endpointID); err != nil {
			t.Logf("cleanup DeleteEndpoint(%q) error: %v", endpointID, err)
		}
	})

	response, found, err := client.GetEndpoint(ctx, endpointID)
	if err != nil {
		t.Fatalf("GetEndpoint(%q) error = %v", endpointID, err)
	}
	if !found {
		t.Fatalf("GetEndpoint(%q) not found right after create", endpointID)
	}
	if response.TemplateID != templateID {
		t.Fatalf("GetEndpoint(%q) templateId = %q, want %q", endpointID, response.TemplateID, templateID)
	}
	if response.WorkersMax != workersMax {
		t.Fatalf("GetEndpoint(%q) workersMax = %d, want %d", endpointID, response.WorkersMax, workersMax)
	}

	newMax := int32(2)
	if err := client.UpdateEndpoint(ctx, endpointID, runpodclient.UpdateEndpointRequest{
		WorkersMax: &newMax,
	}); err != nil {
		t.Fatalf("UpdateEndpoint(%q) error = %v", endpointID, err)
	}

	response, found, err = client.GetEndpoint(ctx, endpointID)
	if err != nil || !found {
		t.Fatalf("GetEndpoint(%q) after update: found=%v err=%v", endpointID, found, err)
	}
	if response.WorkersMax != newMax {
		t.Fatalf("GetEndpoint(%q) workersMax after patch = %d, want %d", endpointID, response.WorkersMax, newMax)
	}

	if err := client.DeleteEndpoint(ctx, endpointID); err != nil {
		t.Fatalf("DeleteEndpoint(%q) error = %v", endpointID, err)
	}
	deleted = true

	for i := 0; i < deletePollAttempts; i++ {
		_, found, err := client.GetEndpoint(ctx, endpointID)
		if err != nil {
			t.Fatalf("GetEndpoint(%q) after delete error = %v", endpointID, err)
		}
		if !found {
			return
		}
		if i < deletePollAttempts-1 {
			time.Sleep(deletePollInterval)
		}
	}

	t.Fatalf("endpoint %s still exists 60 seconds after DeleteEndpoint()", endpointID)
}
