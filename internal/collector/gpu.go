package collector

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// GPUCollector reads metrics from NVIDIA GPUs via NVML.
type GPUCollector struct {
	devices []nvml.Device
	info    []GPUDevice
}

// NewGPUCollector initializes NVML and enumerates GPU devices.
func NewGPUCollector() (*GPUCollector, error) {
	ret := nvml.Init()
	if ret != nvml.SUCCESS {
		return nil, fmt.Errorf("nvml.Init failed: %v", nvml.ErrorString(ret))
	}

	count, ret := nvml.DeviceGetCount()
	if ret != nvml.SUCCESS {
		return nil, fmt.Errorf("DeviceGetCount: %v", nvml.ErrorString(ret))
	}

	driverVer, _ := nvml.SystemGetDriverVersion()

	gc := &GPUCollector{
		devices: make([]nvml.Device, count),
		info:    make([]GPUDevice, count),
	}

	for i := 0; i < count; i++ {
		dev, ret := nvml.DeviceGetHandleByIndex(i)
		if ret != nvml.SUCCESS {
			return nil, fmt.Errorf("DeviceGetHandleByIndex(%d): %v", i, nvml.ErrorString(ret))
		}
		gc.devices[i] = dev

		name, _ := dev.GetName()
		uuid, _ := dev.GetUUID()
		memInfo, _ := dev.GetMemoryInfo()

		gc.info[i] = GPUDevice{
			ID:        i,
			UUID:      uuid,
			Name:      name,
			MemTotal:  memInfo.Total / (1024 * 1024),
			DriverVer: driverVer,
		}
	}

	return gc, nil
}

// Devices returns static device info.
func (gc *GPUCollector) Devices() []GPUDevice {
	return gc.info
}

// gpuDevice is the part of the NVML device API this collector uses. Naming
// it lets a device that answers nothing be tested without a GPU.
type gpuDevice interface {
	GetUtilizationRates() (nvml.Utilization, nvml.Return)
	GetMemoryInfo() (nvml.Memory, nvml.Return)
	GetTemperature(nvml.TemperatureSensors) (uint32, nvml.Return)
	GetFanSpeed() (uint32, nvml.Return)
	GetPowerUsage() (uint32, nvml.Return)
	GetEnforcedPowerLimit() (uint32, nvml.Return)
	GetClockInfo(nvml.ClockType) (uint32, nvml.Return)
	GetPcieThroughput(nvml.PcieUtilCounter) (uint32, nvml.Return)
	GetPerformanceState() (nvml.Pstates, nvml.Return)
	GetEncoderUtilization() (uint32, uint32, nvml.Return)
	GetDecoderUtilization() (uint32, uint32, nvml.Return)
}

// Collect reads current metrics from all GPUs. Devices that answered nothing
// are left out.
func (gc *GPUCollector) Collect() []GPUMetrics {
	now := time.Now().Unix()
	metrics := make([]GPUMetrics, 0, len(gc.devices))

	for i, dev := range gc.devices {
		m, ok := collectDevice(dev, i, now)
		if !ok {
			continue
		}
		metrics = append(metrics, m)
	}

	return metrics
}

// collectDevice reads one device. ok is false when every call failed, which
// is what a driver answering with errors rather than blocking looks like: a
// version mismatch after an upgrade, an Xid, a card off the bus.
//
// Such a sample must not be stored. Every field would be zero, which reads
// on the dashboard exactly like an idle GPU, and its fresh timestamp would
// keep the staleness checks quiet while nothing is being measured at all.
func collectDevice(dev gpuDevice, id int, now int64) (GPUMetrics, bool) {
	m := GPUMetrics{
		Timestamp: now,
		GPUID:     id,
	}
	ok := false

	if util, ret := dev.GetUtilizationRates(); ret == nvml.SUCCESS {
		ok = true
		m.GPUUtil = float64(util.Gpu)
		m.MemUtil = float64(util.Memory)
	}

	if memInfo, ret := dev.GetMemoryInfo(); ret == nvml.SUCCESS {
		ok = true
		m.MemUsed = memInfo.Used / (1024 * 1024)
	}

	if temp, ret := dev.GetTemperature(nvml.TEMPERATURE_GPU); ret == nvml.SUCCESS {
		ok = true
		m.Temperature = int(temp)
	}

	if fan, ret := dev.GetFanSpeed(); ret == nvml.SUCCESS {
		ok = true
		m.FanSpeed = int(fan)
	}

	if power, ret := dev.GetPowerUsage(); ret == nvml.SUCCESS {
		ok = true
		m.PowerDraw = float64(power) / 1000.0 // mW to W
	}

	if limit, ret := dev.GetEnforcedPowerLimit(); ret == nvml.SUCCESS {
		ok = true
		m.PowerLimit = float64(limit) / 1000.0
	}

	if clock, ret := dev.GetClockInfo(nvml.CLOCK_GRAPHICS); ret == nvml.SUCCESS {
		ok = true
		m.ClockGfx = int(clock)
	}

	if clock, ret := dev.GetClockInfo(nvml.CLOCK_MEM); ret == nvml.SUCCESS {
		ok = true
		m.ClockMem = int(clock)
	}

	if tx, ret := dev.GetPcieThroughput(nvml.PCIE_UTIL_TX_BYTES); ret == nvml.SUCCESS {
		ok = true
		m.PCIeTx = int(tx)
	}

	if rx, ret := dev.GetPcieThroughput(nvml.PCIE_UTIL_RX_BYTES); ret == nvml.SUCCESS {
		ok = true
		m.PCIeRx = int(rx)
	}

	if pstate, ret := dev.GetPerformanceState(); ret == nvml.SUCCESS {
		ok = true
		m.PState = int(pstate)
	}

	if util, _, ret := dev.GetEncoderUtilization(); ret == nvml.SUCCESS {
		ok = true
		m.EncoderUtil = float64(util)
	}

	if util, _, ret := dev.GetDecoderUtilization(); ret == nvml.SUCCESS {
		ok = true
		m.DecoderUtil = float64(util)
	}

	return m, ok
}

// CollectProcesses returns GPU processes for all devices.
func (gc *GPUCollector) CollectProcesses() []GPUProcess {
	now := time.Now().Unix()
	var procs []GPUProcess

	for i, dev := range gc.devices {
		infos, ret := dev.GetComputeRunningProcesses()
		if ret != nvml.SUCCESS {
			continue
		}
		for _, info := range infos {
			name := readProcessName(info.Pid)
			procs = append(procs, GPUProcess{
				Timestamp: now,
				GPUID:     i,
				PID:       info.Pid,
				Name:      name,
				GPUMem:    info.UsedGpuMemory / (1024 * 1024),
			})
		}

		// Also check graphics processes
		gfxInfos, ret := dev.GetGraphicsRunningProcesses()
		if ret != nvml.SUCCESS {
			continue
		}
		for _, info := range gfxInfos {
			// Deduplicate with compute processes
			found := false
			for _, p := range procs {
				if p.PID == info.Pid && p.GPUID == i {
					found = true
					break
				}
			}
			if found {
				continue
			}
			name := readProcessName(info.Pid)
			procs = append(procs, GPUProcess{
				Timestamp: now,
				GPUID:     i,
				PID:       info.Pid,
				Name:      name,
				GPUMem:    info.UsedGpuMemory / (1024 * 1024),
			})
		}
	}

	return procs
}

// Shutdown cleans up NVML.
func (gc *GPUCollector) Shutdown() {
	nvml.Shutdown()
}

func readProcessName(pid uint32) string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil {
		return fmt.Sprintf("pid-%d", pid)
	}
	return strings.TrimSpace(string(data))
}
