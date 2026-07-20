package host

import (
	"sync"
	"testing"
)

func TestSignalDoneAndCloseTerminalChannelsDoNotRaceOnClosedChannel(t *testing.T) {
	for i := 0; i < 200; i++ {
		h := &Host{
			done:     make(chan struct{}, 1),
			events:   make(chan Event, 1),
			streamCh: make(chan string, 1),
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			h.signalDone()
		}()
		go func() {
			defer wg.Done()
			h.closeTerminalChannels()
		}()
		wg.Wait()
		// The exact production crash was a late waitDone signal after Close.
		// This must remain a harmless no-op, not send on a closed channel.
		h.signalDone()
	}
}
