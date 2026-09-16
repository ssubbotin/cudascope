package collector

import (
	"context"
	"fmt"
	"log"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// xidWaitMillis bounds one wait. The call blocks until an event arrives or
// the timeout expires, and a bounded wait is what lets cancellation be
// noticed at all.
const xidWaitMillis = 5000

// XidWatcher turns NVML's event stream into callbacks.
//
// An Xid is the driver reporting a fault: a fallen off bus, an ECC error it
// could not correct, a program that hung the card. It arrives as an event
// rather than a reading, which is why it does not travel with the metric
// samples.
type XidWatcher struct {
	set nvml.EventSet
}

// WatchXid registers every device for Xid events. It fails when no device
// accepts registration, which is what a virtualized or too-old driver does.
func (gc *GPUCollector) WatchXid() (*XidWatcher, error) {
	set, ret := nvml.EventSetCreate()
	if ret != nvml.SUCCESS {
		return nil, fmt.Errorf("create event set: %v", nvml.ErrorString(ret))
	}

	registered := 0
	for i, dev := range gc.devices {
		if ret := dev.RegisterEvents(nvml.EventTypeXidCriticalError, set); ret != nvml.SUCCESS {
			log.Printf("cannot watch Xid errors on GPU %d: %v", i, nvml.ErrorString(ret))
			continue
		}
		registered++
	}

	if registered == 0 {
		set.Free()
		return nil, fmt.Errorf("no device accepted Xid registration")
	}

	log.Printf("watching Xid errors on %d GPU(s)", registered)
	return &XidWatcher{set: set}, nil
}

// Run reports Xid errors until ctx is cancelled. It runs on its own
// goroutine for the same reason every metric source does: Wait is a
// blocking cgo call, and one of those must never hold up the rest.
func (w *XidWatcher) Run(ctx context.Context, onXid func(gpuID int, xid uint64)) {
	defer w.set.Free()

	for {
		if ctx.Err() != nil {
			return
		}

		data, ret := w.set.Wait(xidWaitMillis)
		switch ret {
		case nvml.SUCCESS:
		case nvml.ERROR_TIMEOUT:
			continue
		default:
			log.Printf("Xid watch stopped: %v", nvml.ErrorString(ret))
			return
		}

		if data.EventType != nvml.EventTypeXidCriticalError {
			continue
		}

		gpuID := -1
		if data.Device != nil {
			if index, ret := data.Device.GetIndex(); ret == nvml.SUCCESS {
				gpuID = index
			}
		}

		onXid(gpuID, data.EventData)
	}
}
