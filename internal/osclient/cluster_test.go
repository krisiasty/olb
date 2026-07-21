package osclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gophercloud/gophercloud/v2"
)

func TestListCOEClustersUsesSinglePaginatedList(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/clusters" {
			t.Errorf("request path = %q, want /v1/clusters", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"clusters":[{
            "uuid":"71244d81-5d8c-4228-9fdc-793fde6c27b7",
			"name":"clusterapi","stack_id":"kube-slzjy",
            "cluster_template_id":"template-1","keypair":"Openstack Admin",
            "node_count":3,"master_count":3,"flavor_id":"worker-flavor",
            "master_flavor_id":"master-flavor","status":"UPDATE_COMPLETE",
            "health_status":"HEALTHY","labels":{"kube_tag":"v1.32.8"}
        }]}`))
	}))
	defer server.Close()

	client := &gophercloud.ServiceClient{
		ProviderClient: &gophercloud.ProviderClient{},
		Endpoint:       server.URL + "/v1/",
	}
	c := &Clients{activeServices: &serviceClients{container: client}}
	items, err := c.ListCOEClusters(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("Magnum requests = %d, want one cluster-list request", calls)
	}
	if len(items) != 1 {
		t.Fatalf("clusters = %+v", items)
	}
	got := items[0]
	if got.UUID != "71244d81-5d8c-4228-9fdc-793fde6c27b7" || got.Name != "clusterapi" || got.StackID != "kube-slzjy" {
		t.Fatalf("cluster identity = %+v", got)
	}
	if got.HealthStatus != "HEALTHY" || got.Status != "UPDATE_COMPLETE" || got.Labels["kube_tag"] != "v1.32.8" {
		t.Fatalf("cluster state = %+v", got)
	}
	if got.ProjectID != "" {
		t.Fatalf("cluster-list project ID = %q, want absent value preserved", got.ProjectID)
	}
}

func TestListCOEClustersReportsMissingMagnum(t *testing.T) {
	c := &Clients{activeServices: &serviceClients{}}
	if _, err := c.ListCOEClusters(context.Background()); err == nil {
		t.Fatal("missing Magnum client should return an error")
	}
}
