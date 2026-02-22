//go:build windows

package main

import (
	"log"
	"time"

	"golang.org/x/sys/windows/svc"
)

type clawpanelService struct{}

func (m *clawpanelService) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (ssec bool, errno uint32) {
	const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}

	// Start the server in a goroutine
	stopCh := make(chan struct{})
	go runServer(stopCh)

	changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}

	for c := range r {
		switch c.Cmd {
		case svc.Interrogate:
			changes <- c.CurrentStatus
			time.Sleep(100 * time.Millisecond)
			changes <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			changes <- svc.Status{State: svc.StopPending}
			close(stopCh)
			return false, 0
		default:
			log.Printf("[ClawPanel] unexpected control request #%d", c)
		}
	}
	return false, 0
}

// runAsService detects if running as a Windows service and runs accordingly.
// Returns true if running as a service (caller should not start server manually).
func runAsService() bool {
	isService, err := svc.IsWindowsService()
	if err != nil {
		log.Printf("[ClawPanel] failed to detect service mode: %v", err)
		return false
	}
	if !isService {
		return false
	}

	// svc.Run blocks until the service stops
	err = svc.Run("ClawPanel", &clawpanelService{})
	if err != nil {
		log.Fatalf("[ClawPanel] service run failed: %v", err)
	}
	return true
}
