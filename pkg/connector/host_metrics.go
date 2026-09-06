package connector

import (
	"time"

	pb "github.com/kabili207/meshtastic-go/core/proto"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
)

// buildHostMetrics gathers host telemetry from the local machine. It is wired
// into the bridge as the HostMetricsProvider so the meshtastic-go library does
// not need a dependency on gopsutil.
func (c *MeshtasticConnector) buildHostMetrics() (*pb.Telemetry, error) {
	now := time.Now().UTC()

	meminfo, err := mem.VirtualMemory()
	if err != nil {
		return nil, err
	}
	loadAvg, err := load.Avg()
	if err != nil {
		return nil, err
	}
	uptime, err := host.Uptime()
	if err != nil {
		return nil, err
	}
	diskUsage, err := disk.Usage("/")
	if err != nil {
		return nil, err
	}

	return &pb.Telemetry{
		Time: uint32(now.Unix()),
		Variant: &pb.Telemetry_HostMetrics{
			HostMetrics: &pb.HostMetrics{
				UptimeSeconds:  uint32(uptime),
				Load1:          uint32(loadAvg.Load1 * 100),
				Load5:          uint32(loadAvg.Load5 * 100),
				Load15:         uint32(loadAvg.Load15 * 100),
				FreememBytes:   meminfo.Available,
				Diskfree1Bytes: diskUsage.Free,
			},
		},
	}, nil
}
