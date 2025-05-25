package connectors

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

var _ MeshConnector = (*udpMessageHandler)(nil)

type udpMessageHandler struct {
	conn         *net.UDPConn
	handlerFunc  MeshPacketHandler
	stateFunc    StateEventHandler
	running      atomic.Bool
	listening    atomic.Bool
	stopChan     chan struct{}
	waitGroup    sync.WaitGroup
	reconnectMux sync.Mutex
	logger       zerolog.Logger
	isRestart    bool
}

// NewUDPMessageHandler creates a new UDPMessageHandler with optional zerolog.Logger
func NewUDPMessageHandler(logger zerolog.Logger) MeshConnector {
	return &udpMessageHandler{
		stopChan: make(chan struct{}),
		logger:   logger,
	}
}

// Start begins listening to multicast packets
func (h *udpMessageHandler) Start() error {
	if h.running.Load() {
		return nil
	}
	h.running.Store(true)
	h.stopChan = make(chan struct{})

	h.waitGroup.Add(1)
	go h.listenWithReconnect()
	return nil
}

// Stop halts the listener and cleans up
func (h *udpMessageHandler) Stop() {
	if !h.running.Load() {
		return
	}
	h.running.Store(false)
	h.listening.Store(false)
	close(h.stopChan)
	h.waitGroup.Wait()
	h.closeConn()
}

func (h *udpMessageHandler) IsRunning() bool {
	return h.running.Load()
}

func (h *udpMessageHandler) IsConnected() bool {
	return h.listening.Load()
}

// AddChannel implements MeshHandler.
func (h *udpMessageHandler) AddChannel(channelName string) {
	// Do nothing; not required for this connector
}

// SetHandler registers the callback for incoming messages
func (h *udpMessageHandler) SetPacketHandler(fn MeshPacketHandler) {
	h.handlerFunc = fn
}

// SetStateHandler implements MeshHandler.
func (h *udpMessageHandler) SetStateHandler(fn StateEventHandler) {
	h.stateFunc = fn
}

// SendPacket implements MeshHandler.
func (h *udpMessageHandler) SendPacket(channel string, packet *pb.MeshPacket) error {
	return h.SendMulticast(packet)
}

// SendMulticast sends a protobuf message to the multicast group
func (h *udpMessageHandler) SendMulticast(msg *pb.MeshPacket) error {
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

func (h *udpMessageHandler) listenWithReconnect() {
	defer h.waitGroup.Done()
	delay := 1 * time.Second

	for h.running.Load() {
		err := h.setupSocket()
		if err != nil {
			h.logger.Warn().Err(err).Msg("UDP setup failed")
			h.listening.Store(false)
			h.emitStateEvent(EventConnectionLost)
			time.Sleep(delay)
			delay = minDuration(delay*2, maxReconnectDelay)
			continue
		}

		h.listening.Store(true)

		eventType := EventStarted
		if h.isRestart {
			eventType = EventRestarted
		}
		h.emitStateEvent(eventType)

		h.isRestart = true

		h.logger.Info().Str("addr", h.conn.LocalAddr().String()).Msg("Listening for UDP multicast")

		if h.listenLoop() == nil {
			break // graceful shutdown
		}

		h.logger.Warn().Dur("retry_in", delay).Msg("UDP listener restarting")
		h.closeConn()
		h.listening.Store(false)
		h.emitStateEvent(EventConnectionLost)
		time.Sleep(delay)
		delay = minDuration(delay*2, maxReconnectDelay)
	}

	h.listening.Store(false)
}

// listenLoop handles message reading
func (h *udpMessageHandler) listenLoop() error {
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
				// TODO: Determine gateway node based on the final byte in packet.RelayNode
				h.handlerFunc(NetworkMeshPacket{MeshPacket: msg, Source: PacketSourceUDP})
			}
		}
	}
}

// setupSocket joins multicast group and prepares socket
func (h *udpMessageHandler) setupSocket() error {
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
func (h *udpMessageHandler) closeConn() {
	h.reconnectMux.Lock()
	defer h.reconnectMux.Unlock()
	if h.conn != nil {
		_ = h.conn.Close()
		h.conn = nil
	}
}

func (h *udpMessageHandler) emitStateEvent(eventType ListenerEvent) {
	if h.stateFunc != nil {
		h.stateFunc(h, eventType)
	}
}

// minDuration returns the smaller of a or b
func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
