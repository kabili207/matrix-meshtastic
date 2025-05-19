package mesh

import (
	"net"
	"sync"
	"sync/atomic"
	"time"

	pb "github.com/meshnet-gophers/meshtastic-go/meshtastic"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/proto"
)

const (
	MulticastIP   = "224.0.0.69"
	MulticastPort = 4403

	maxReconnectDelay = 30 * time.Second
)

type UDPMessageHandler struct {
	conn         *net.UDPConn
	handlerFunc  func(*pb.MeshPacket)
	running      atomic.Bool
	stopChan     chan struct{}
	waitGroup    sync.WaitGroup
	reconnectMux sync.Mutex
	logger       zerolog.Logger
}

// NewUDPMessageHandler creates a new UDPMessageHandler with optional zerolog.Logger
func NewUDPMessageHandler(logger zerolog.Logger) *UDPMessageHandler {
	return &UDPMessageHandler{
		stopChan: make(chan struct{}),
		logger:   logger,
	}
}

// Start begins listening to multicast packets
func (h *UDPMessageHandler) Start() {
	if h.running.Load() {
		return
	}
	h.running.Store(true)
	h.stopChan = make(chan struct{})

	h.waitGroup.Add(1)
	go h.listenWithReconnect()
}

// Stop halts the listener and cleans up
func (h *UDPMessageHandler) Stop() {
	if !h.running.Load() {
		return
	}
	h.running.Store(false)
	close(h.stopChan)
	h.waitGroup.Wait()
	h.closeConn()
}

// SetHandler registers the callback for incoming messages
func (h *UDPMessageHandler) SetHandler(fn func(*pb.MeshPacket)) {
	h.handlerFunc = fn
}

// SendMulticast sends a protobuf message to the multicast group
func (h *UDPMessageHandler) SendMulticast(msg *pb.MeshPacket) error {
	data, err := proto.Marshal(msg)
	if err != nil {
		return err
	}

	dst := &net.UDPAddr{
		IP:   net.ParseIP(MulticastIP),
		Port: MulticastPort,
	}

	conn, err := net.DialUDP("udp", nil, dst)
	if err != nil {
		return err
	}
	defer conn.Close()

	_, err = conn.Write(data)
	return err
}

func (h *UDPMessageHandler) listenWithReconnect() {
	defer h.waitGroup.Done()
	delay := 1 * time.Second

	for h.running.Load() {
		err := h.setupSocket()
		if err != nil {
			h.logger.Warn().Err(err).Msg("UDP setup failed")
			time.Sleep(delay)
			delay = minDuration(delay*2, maxReconnectDelay)
			continue
		}

		h.logger.Info().Str("addr", h.conn.LocalAddr().String()).Msg("Listening for UDP multicast")
		if h.listenLoop() == nil {
			break // graceful shutdown
		}

		h.logger.Warn().Dur("retry_in", delay).Msg("UDP listener restarting")
		h.closeConn()
		time.Sleep(delay)
		delay = minDuration(delay*2, maxReconnectDelay)
	}
}

// listenLoop handles message reading
func (h *UDPMessageHandler) listenLoop() error {
	buf := make([]byte, 2048)
	for {
		select {
		case <-h.stopChan:
			return nil
		default:
			n, _, err := h.conn.ReadFromUDP(buf)
			if err != nil {
				h.logger.Error().Err(err).Msg("Read error")
				return err
			}

			msg := &pb.MeshPacket{}
			if err := proto.Unmarshal(buf[:n], msg); err != nil {
				h.logger.Warn().Err(err).Msg("Unmarshal error")
				continue
			}

			if h.handlerFunc != nil {
				h.handlerFunc(msg)
			}
		}
	}
}

// setupSocket joins multicast group and prepares socket
func (h *UDPMessageHandler) setupSocket() error {
	h.reconnectMux.Lock()
	defer h.reconnectMux.Unlock()

	group := &net.UDPAddr{
		IP:   net.ParseIP(MulticastIP),
		Port: MulticastPort,
	}

	conn, err := net.ListenMulticastUDP("udp", nil, group)
	if err != nil {
		return err
	}
	if err := conn.SetReadBuffer(2048); err != nil {
		h.logger.Warn().Err(err).Msg("SetReadBuffer failed")
	}
	h.conn = conn
	return nil
}

// closeConn safely closes socket
func (h *UDPMessageHandler) closeConn() {
	h.reconnectMux.Lock()
	defer h.reconnectMux.Unlock()
	if h.conn != nil {
		_ = h.conn.Close()
		h.conn = nil
	}
}

// minDuration returns the smaller of a or b
func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
