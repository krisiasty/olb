package osclient

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/placement/v1/resourceproviders"
)

const (
	managedPCITrait               = "COMPUTE_MANAGED_PCI_DEVICE"
	acceleratorRequestConcurrency = 6
)

var pciAddressSuffix = regexp.MustCompile(`(?i)([0-9a-f]{4}:[0-9a-f]{2}:[0-9a-f]{2}\.[0-7])$`)

// AcceleratorAllocation is one Placement consumer using an accelerator
// resource. For ordinary steady-state allocations ConsumerID is a Nova server
// UUID; migrations and other consumers are deliberately left generic.
type AcceleratorAllocation struct {
	ConsumerID string `json:"consumer_id" yaml:"consumer_id"`
	Amount     int    `json:"amount" yaml:"amount"`
}

// Accelerator is one resource-class inventory exposed by a Nova-managed PCI
// resource provider beneath a compute host.
type Accelerator struct {
	ProviderID      string                  `json:"provider_id" yaml:"provider_id"`
	ProviderName    string                  `json:"provider_name" yaml:"provider_name"`
	PCIAddress      string                  `json:"pci_address,omitempty" yaml:"pci_address,omitempty"`
	ResourceClass   string                  `json:"resource_class" yaml:"resource_class"`
	DisplayName     string                  `json:"display_name" yaml:"display_name"`
	Total           int                     `json:"total" yaml:"total"`
	Reserved        int                     `json:"reserved" yaml:"reserved"`
	Used            int                     `json:"used" yaml:"used"`
	AllocationRatio float32                 `json:"allocation_ratio" yaml:"allocation_ratio"`
	Allocations     []AcceleratorAllocation `json:"allocations,omitempty" yaml:"allocations,omitempty"`
}

// ListHypervisorAccelerators returns the Nova-managed PCI inventories in the
// Placement provider tree rooted at hypervisorID. The server-side trait filter
// avoids mistaking NUMA, storage, or network child providers for accelerators.
// Per-provider inventory/allocation requests run through a bounded worker pool
// so a host with many devices does not serialize dozens of round trips or
// overwhelm Placement.
func (c *Clients) ListHypervisorAccelerators(ctx context.Context, hypervisorID string) ([]Accelerator, error) {
	sc, err := c.activeClients()
	if err != nil {
		return nil, err
	}
	if sc.placement == nil {
		return nil, fmt.Errorf("placement: %w", ErrUnavailable)
	}
	if strings.TrimSpace(hypervisorID) == "" {
		return nil, fmt.Errorf("placement: hypervisor UUID is required")
	}

	placement := *sc.placement
	placement.Microversion = "1.18"
	pages, err := resourceproviders.List(&placement, resourceproviders.ListOpts{
		InTree:   hypervisorID,
		Required: managedPCITrait,
	}).AllPages(ctx)
	if err != nil {
		return nil, acceleratorAPIError(err)
	}
	providers, err := resourceproviders.ExtractResourceProviders(pages)
	if err != nil {
		return nil, err
	}
	if len(providers) == 0 {
		return nil, nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make([][]Accelerator, len(providers))
	jobs := make(chan int)
	workers := min(acceleratorRequestConcurrency, len(providers))

	var wg sync.WaitGroup
	var errOnce sync.Once
	var firstErr error
	setError := func(err error) {
		errOnce.Do(func() {
			firstErr = err
			cancel()
		})
	}

	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for index := range jobs {
				provider := providers[index]
				inventories, err := resourceproviders.GetInventories(ctx, &placement, provider.UUID).Extract()
				if err != nil {
					setError(fmt.Errorf("list accelerator inventories for %s: %w", provider.UUID, acceleratorAPIError(err)))
					continue
				}
				allocations, err := resourceproviders.GetAllocations(ctx, &placement, provider.UUID).Extract()
				if err != nil {
					setError(fmt.Errorf("list accelerator allocations for %s: %w", provider.UUID, acceleratorAPIError(err)))
					continue
				}
				results[index] = acceleratorsFromProvider(provider, inventories, allocations)
			}
		}()
	}

sendJobs:
	for index := range providers {
		select {
		case jobs <- index:
		case <-ctx.Done():
			break sendJobs
		}
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var out []Accelerator
	for _, providerRows := range results {
		out = append(out, providerRows...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PCIAddress != out[j].PCIAddress {
			return out[i].PCIAddress < out[j].PCIAddress
		}
		if out[i].ProviderName != out[j].ProviderName {
			return out[i].ProviderName < out[j].ProviderName
		}
		return out[i].ResourceClass < out[j].ResourceClass
	})
	return out, nil
}

func acceleratorsFromProvider(
	provider resourceproviders.ResourceProvider,
	inventories *resourceproviders.ResourceProviderInventories,
	allocations *resourceproviders.ResourceProviderAllocations,
) []Accelerator {
	classes := make([]string, 0, len(inventories.Inventories))
	for resourceClass := range inventories.Inventories {
		classes = append(classes, resourceClass)
	}
	sort.Strings(classes)

	rows := make([]Accelerator, 0, len(classes))
	for _, resourceClass := range classes {
		inventory := inventories.Inventories[resourceClass]
		row := Accelerator{
			ProviderID: provider.UUID, ProviderName: provider.Name,
			PCIAddress:    acceleratorPCIAddress(provider.Name),
			ResourceClass: resourceClass, DisplayName: acceleratorDisplayName(resourceClass),
			Total: inventory.Total, Reserved: inventory.Reserved,
			AllocationRatio: inventory.AllocationRatio,
		}
		for consumerID, allocation := range allocations.Allocations {
			amount := allocation.Resources[resourceClass]
			if amount <= 0 {
				continue
			}
			row.Used += amount
			row.Allocations = append(row.Allocations, AcceleratorAllocation{
				ConsumerID: consumerID,
				Amount:     amount,
			})
		}
		sort.Slice(row.Allocations, func(i, j int) bool {
			return row.Allocations[i].ConsumerID < row.Allocations[j].ConsumerID
		})
		rows = append(rows, row)
	}
	return rows
}

func acceleratorPCIAddress(providerName string) string {
	match := pciAddressSuffix.FindStringSubmatch(strings.TrimSpace(providerName))
	if len(match) != 2 {
		return ""
	}
	return strings.ToUpper(match[1])
}

func acceleratorDisplayName(resourceClass string) string {
	name := strings.TrimPrefix(strings.TrimSpace(resourceClass), "CUSTOM_")
	name = strings.TrimPrefix(name, "GPU_")
	name = strings.ReplaceAll(name, "_", " ")
	if name == "" {
		return resourceClass
	}
	return name
}

func acceleratorAPIError(err error) error {
	if gophercloud.ResponseCodeIs(err, 403) {
		return ErrAdminRequired
	}
	return err
}
