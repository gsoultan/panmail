package usecases

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// samplingRepo counts resource samples and reports a fixed size.
type samplingRepo struct {
	mockEventRepo
	samples atomic.Int64
	size    int64
}

func (s *samplingRepo) WriteResourceMetric(context.Context, float64, uint64, float64) error {
	s.samples.Add(1)
	return nil
}

func (s *samplingRepo) DiskUsage() int64 { return s.size }

type sizedInbound struct {
	mockInboundRepo
	size int64
}

func (s *sizedInbound) DiskUsage() int64 { return s.size }

type sized int64

func (s sized) DiskUsage() int64 { return int64(s) }

// The tracker ran for the life of the process with no way to stop it, writing
// resource samples into a store that shutdown was closing. Close stops it and
// waits until it has.
func TestCloseStopsThePerformanceTracker(t *testing.T) {
	previous := [2]time.Duration{performanceInterval, resourceInterval}
	performanceInterval, resourceInterval = time.Millisecond, time.Millisecond
	t.Cleanup(func() { performanceInterval, resourceInterval = previous[0], previous[1] })

	repo := &samplingRepo{}
	uc := NewProcessEventUsecase(repo, nil, nil, nil, nil).(*processEventUsecase)

	deadline := time.Now().Add(5 * time.Second)
	for repo.samples.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if repo.samples.Load() == 0 {
		t.Fatal("the tracker never sampled, so stopping it proves nothing")
	}

	done := make(chan struct{})
	go func() { _ = uc.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return: the tracker is still running")
	}

	after := repo.samples.Load()
	time.Sleep(50 * time.Millisecond)
	if got := repo.samples.Load(); got != after {
		t.Errorf("%d samples written after Close", got-after)
	}
	if err := uc.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// Disk usage is what the stores report about themselves, not a walk of three
// directory names relative to wherever the process was started.
func TestDiskUsageComesFromTheStores(t *testing.T) {
	uc := NewProcessEventUsecase(&samplingRepo{size: 100}, &sizedInbound{size: 20}, nil, nil, nil).(*processEventUsecase)
	t.Cleanup(func() { _ = uc.Close() })
	uc.AddDiskUsageSource(sized(3))

	got, err := uc.GetPerformanceMetrics(t.Context())
	if err != nil {
		t.Fatalf("GetPerformanceMetrics: %v", err)
	}
	if got.DiskUsage != 123 {
		t.Errorf("disk usage = %d; want the three stores' own figures added, 123", got.DiskUsage)
	}
}
