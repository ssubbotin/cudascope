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

// Collect reads current metrics from all GPUs.
func (gc *GPUCollector) Collect() []GPUMetrics {
	now := time.Now().Unix()
	metrics := make([]GPUMetrics, len(gc.devices))

	for i, dev := range gc.devices {
		m := GPUMetrics{
			Timestamp: now,
			GPUID:     i,
		}

		if util, ret := dev.GetUtilizationRates(); ret == nvml.SUCCESS {
			m.GPUUtil = float64(util.Gpu)
			m.MemUtil = float64(util.Memory)
		}

		if memInfo, ret := dev.GetMemoryInfo(); ret == nvml.SUCCESS {
			m.MemUsed = memInfo.Used / (1024 * 1024)
		}

		if temp, ret := dev.GetTemperature(nvml.TEMPERATURE_GPU); ret == nvml.SUCCESS {
			m.Temperature = int(temp)
		}

		if fan, ret := dev.GetFanSpeed(); ret == nvml.SUCCESS {
			m.FanSpeed = int(fan)
		}

		if power, ret := dev.GetPowerUsage(); ret == nvml.SUCCESS {
			m.PowerDraw = float64(power) / 1000.0 // mW to W
		}

		if limit, ret := dev.GetEnforcedPowerLimit(); ret == nvml.SUCCESS {
			m.PowerLimit = float64(limit) / 1000.0
		}

		if clock, ret := dev.GetClockInfo(nvml.CLOCK_GRAPHICS); ret == nvml.SUCCESS {
			m.ClockGfx = int(clock)
		}

		if clock, ret := dev.GetClockInfo(nvml.CLOCK_MEM); ret == nvml.SUCCESS {
			m.ClockMem = int(clock)
		}

		if tx, ret := dev.GetPcieThroughput(nvml.PCIE_UTIL_TX_BYTES); ret == nvml.SUCCESS {
			m.PCIeTx = int(tx)
		}

		if rx, ret := dev.GetPcieThroughput(nvml.PCIE_UTIL_RX_BYTES); ret == nvml.SUCCESS {
			m.PCIeRx = int(rx)
		}

		if pstate, ret := dev.GetPerformanceState(); ret == nvml.SUCCESS {
			m.PState = int(pstate)
		}

		if util, _, ret := dev.GetEncoderUtilization(); ret == nvml.SUCCESS {
			m.EncoderUtil = float64(util)
		}

		if util, _, ret := dev.GetDecoderUtilization(); ret == nvml.SUCCESS {
			m.DecoderUtil = float64(util)
		}

		metrics[i] = m
	}

	return metrics
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
