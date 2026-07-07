package natneg

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"fmt"
	"net"
	"os"
	"sync"
	"time"
	"wwfc/common"
	"wwfc/logging"
	"wwfc/nhttp"

	"github.com/logrusorgru/aurora/v3"
)

const (
	SessionTTL            = 30 * time.Second
	ConnectFastRetryDelay = 75 * time.Millisecond
	ConnectRetryDelay     = 150 * time.Millisecond
	ConnectRetryLimit     = 48
	ConnectPairDeadline   = 10 * time.Second
	ConnectInitialBurst   = 3

	NNInitRequest         = 0x00
	NNInitReply           = 0x01
	NNErtTestRequest      = 0x02
	NNErtTestReply        = 0x03
	NNStateUpdate         = 0x04
	NNConnectRequest      = 0x05
	NNConnectReply        = 0x06
	NNConnectPing         = 0x07
	NNBackupTestRequest   = 0x08
	NNBackupTestReply     = 0x09
	NNAddressCheckRequest = 0x0A
	NNAddressCheckReply   = 0x0B
	NNNatifyRequest       = 0x0C
	NNReportRequest       = 0x0D
	NNReportReply         = 0x0E
	NNPreInitRequest      = 0x0F
	NNPreInitReply        = 0x10

	// Port type
	PortTypeGamePort = 0x00
	PortTypeNATNEG1  = 0x01
	PortTypeNATNEG2  = 0x02
	PortTypeNATNEG3  = 0x03

	// NAT type
	NATTypeNoNat              = 0x00
	NATTypeFirewallOnly       = 0x01
	NATTypeFullCone           = 0x02
	NATTypeRestrictedCone     = 0x03
	NATTypePortRestrictedCone = 0x04
	NATTypeSymmetric          = 0x05
	NATTypeUnknown            = 0x06

	// NAT mapping scheme
	NATMappingUnknown           = 0x00
	NATMappingSamePrivatePublic = 0x01
	NATMappingConsistent        = 0x02
	NATMappingIncremental       = 0x03
	NATMappingMixed             = 0x04
)

type NATNEGSession struct {
	Open    bool
	Version byte
	Cookie  uint32
	mutex   sync.RWMutex
	Clients map[byte]*NATNEGClient
	Pairs   map[uint16]*NATNEGPair
}

type NATNEGClient struct {
	Cookie          uint32
	Index           byte
	ConnectingIndex byte
	ConnectAck      bool
	Result          map[byte]byte
	PairResults     map[byte]byte
	NegotiateIP     string
	LocalIP         string
	ServerIP        string
	GameName        string
	NATType         byte
	MappingScheme   byte
	LastSeen        time.Time
	LastPortType    byte
	UseGamePort     byte
}

type NATNEGPair struct {
	A             byte
	B             byte
	StartTime     time.Time
	LastSend      time.Time
	RetryCount    int
	Ack           map[byte]bool
	Result        map[byte]byte
	Reported      bool
	TimeoutLogged bool
}

var (
	sessions   = map[uint32]*NATNEGSession{}
	mutex      = sync.RWMutex{}
	natnegConn net.PacketConn

	inShutdown nhttp.AtomicBool
	waitGroup  = sync.WaitGroup{}
)

func StartServer(reload bool) {
	// Get config
	config := common.GetConfig()

	address := *config.GameSpyAddress + ":27901"
	conn, err := net.ListenPacket("udp", address)
	if err != nil {
		panic(err)
	}

	natnegConn = conn
	inShutdown.SetFalse()

	if reload {
		// Load state
		file, err := os.Open("state/natneg_sessions.gob")
		if err != nil {
			panic(err)
		}

		decoder := gob.NewDecoder(file)

		err = decoder.Decode(&sessions)
		file.Close()

		if err != nil {
			panic(err)
		}

		for _, session := range sessions {
			cur := session
			cur.normalize()
			time.AfterFunc(SessionTTL, func() {
				closeSession("NATNEG:"+fmt.Sprintf("%08x", cur.Cookie), cur)
			})
		}

		logging.Notice("NATNEG", "Loaded", aurora.Cyan(len(sessions)), "sessions")
	}

	waitGroup.Add(1)

	go func() {
		defer waitGroup.Done()

		// Close the listener when the application closes.
		defer conn.Close()
		logging.Notice("NATNEG", "Listening on", aurora.BrightCyan(address))

		for {
			if inShutdown.IsSet() {
				return
			}

			buffer := make([]byte, 1024)
			size, addr, err := conn.ReadFrom(buffer)
			if err != nil {
				continue
			}

			waitGroup.Add(1)

			go handleConnection(conn, addr, buffer[:size])
		}
	}()
}

func Shutdown() {
	inShutdown.SetTrue()
	natnegConn.Close()
	waitGroup.Wait()

	// Save state
	mutex.Lock()
	defer mutex.Unlock()

	file, err := os.OpenFile("state/natneg_sessions.gob", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		panic(err)
	}

	encoder := gob.NewEncoder(file)

	err = encoder.Encode(sessions)
	file.Close()

	if err != nil {
		panic(err)
	}

	logging.Notice("NATNEG", "Saved", aurora.Cyan(len(sessions)), "sessions")
}

func handleConnection(conn net.PacketConn, addr net.Addr, buffer []byte) {
	defer waitGroup.Done()

	// Validate the packet magic
	if len(buffer) < 12 || !bytes.Equal(buffer[:6], []byte{0xfd, 0xfc, 0x1e, 0x66, 0x6a, 0xb2}) {
		logging.Error("NATNEG:"+addr.String(), "Invalid packet header")
		return
	}

	// Parse the NATNEG header
	// fd fc 1e 66 6a b2 - Packet Magic
	// xx                - Version
	// xx                - Packet Type / Command
	// xx xx xx xx       - Cookie

	version := buffer[6]
	command := buffer[7]
	cookie := binary.BigEndian.Uint32(buffer[8:12])

	moduleName := "NATNEG:" + fmt.Sprintf("%08x/", cookie) + addr.String()

	var session *NATNEGSession

	if command != NNNatifyRequest && command != NNAddressCheckRequest {
		mutex.Lock()
		var exists bool
		session, exists = sessions[cookie]
		if !exists {
			logging.Info(moduleName, "Creating session")
			session = &NATNEGSession{
				Open:    true,
				Version: version,
				Cookie:  cookie,
				mutex:   sync.RWMutex{},
				Clients: map[byte]*NATNEGClient{},
				Pairs:   map[uint16]*NATNEGPair{},
			}
			sessions[cookie] = session

			// Session has TTL of 30 seconds
			time.AfterFunc(SessionTTL, func() {
				closeSession(moduleName, session)
			})
		}
		mutex.Unlock()

		if session.Version != version {
			logging.Error(moduleName, "Version mismatch")
			return
		}

		session.mutex.Lock()
		defer session.mutex.Unlock()
	}

	switch command {
	default:
		logging.Error(moduleName, "Received unknown command type:", aurora.Cyan(command))

	case NNInitRequest:
		// logging.Info(moduleName, "Command:", aurora.Yellow("NN_INIT"))
		session.handleInit(conn, addr, buffer[12:], moduleName, version)

	case NNInitReply:
		logging.Warn(moduleName, "Received server command:", aurora.Yellow("NN_INITACK"))

	case NNErtTestRequest:
		session.handleERTTest(conn, addr, buffer[12:], moduleName, version)

	case NNErtTestReply:
		session.handleERTAck(addr, buffer[12:], moduleName)

	case NNStateUpdate:
		session.handleStateUpdate(addr, buffer[12:], moduleName)

	case NNConnectRequest:
		logging.Warn(moduleName, "Received server command:", aurora.Yellow("NN_CONNECT"))

	case NNConnectReply:
		// logging.Info(moduleName, "Command:", aurora.Yellow("NN_CONNECT_ACK"))
		session.handleConnectReply(conn, addr, buffer[12:], moduleName, version)

	case NNConnectPing:
		session.handleConnectPing(addr, buffer[12:], moduleName)

	case NNBackupTestRequest:
		session.handleBackupTest(conn, addr, buffer[12:], moduleName, version)

	case NNBackupTestReply:
		session.handleBackupAck(addr, buffer[12:], moduleName)

	case NNAddressCheckRequest:
		handleAddressCheck(conn, addr, cookie, version, buffer[12:], moduleName)

	case NNAddressCheckReply:
		logging.Warn(moduleName, "Received server command:", aurora.Yellow("NN_ADDRESS_REPLY"))

	case NNNatifyRequest:
		handleNatify(conn, addr, cookie, version, buffer[12:], moduleName)

	case NNReportRequest:
		// logging.Info(moduleName, "Command:", aurora.Yellow("NN_REPORT"))
		session.handleReport(conn, addr, buffer[12:], moduleName, version)

	case NNReportReply:
		logging.Warn(moduleName, "Received server command:", aurora.Yellow("NN_REPORT_ACK"))

	case NNPreInitRequest:
		logging.Info(moduleName, "Command:", aurora.Yellow("NN_PREINIT"))
		session.handlePreinit(conn, addr, buffer[12:], moduleName, version)

	case NNPreInitReply:
		logging.Warn(moduleName, "Received server command:", aurora.Yellow("NN_PREINIT_ACK"))
	}
}

func closeSession(moduleName string, session *NATNEGSession) {
	mutex.Lock()
	if inShutdown.IsSet() {
		mutex.Unlock()
		return
	}

	session.Open = false
	delete(sessions, session.Cookie)
	mutex.Unlock()

	session.mutex.Lock()
	defer session.mutex.Unlock()

	// Disconnect each client
	for _, client := range session.Clients {
		for peerIndex, result := range client.PairResults {
			if result != 0 {
				continue
			}
			logging.Info("NATNEG", "Timing out client", aurora.Cyan(client.Index), "peer", aurora.Cyan(peerIndex))
			sendReportCancel(session.Version, session.Cookie, client)
		}
	}

	logging.Info("NATNEG", "Deleted session")
}

func getPortTypeName(portType byte) string {
	switch portType {
	default:
		return fmt.Sprintf("Unknown (0x%02x)", portType)

	case PortTypeGamePort:
		return "GamePort"

	case PortTypeNATNEG1:
		return "NATNEG1"

	case PortTypeNATNEG2:
		return "NATNEG2"

	case PortTypeNATNEG3:
		return "NATNEG3"
	}
}

func (client *NATNEGClient) isMapped() bool {
	if client.NegotiateIP == "" || client.ServerIP == "" {
		return false
	}

	return true
}

func (session *NATNEGSession) normalize() {
	if session.Clients == nil {
		session.Clients = map[byte]*NATNEGClient{}
	}
	if session.Pairs == nil {
		session.Pairs = map[uint16]*NATNEGPair{}
	}
	for index, client := range session.Clients {
		if client == nil {
			delete(session.Clients, index)
			continue
		}
		client.normalize()
	}
}

func (client *NATNEGClient) normalize() {
	if client.Result == nil {
		client.Result = map[byte]byte{}
	}
	if client.PairResults == nil {
		client.PairResults = map[byte]byte{}
	}
	if client.NATType == 0 && client.MappingScheme == 0 {
		client.NATType = NATTypeUnknown
		client.MappingScheme = NATMappingUnknown
	}
}

func pairKey(a, b byte) uint16 {
	if a > b {
		a, b = b, a
	}
	return (uint16(a) << 8) | uint16(b)
}

func getCommandName(command byte) string {
	switch command {
	case NNInitRequest:
		return "NN_INIT"
	case NNInitReply:
		return "NN_INITACK"
	case NNErtTestRequest:
		return "NN_ERTTEST"
	case NNErtTestReply:
		return "NN_ERTACK"
	case NNStateUpdate:
		return "NN_STATEUPDATE"
	case NNConnectRequest:
		return "NN_CONNECT"
	case NNConnectReply:
		return "NN_CONNECT_ACK"
	case NNConnectPing:
		return "NN_CONNECT_PING"
	case NNBackupTestRequest:
		return "NN_BACKUP_TEST"
	case NNBackupTestReply:
		return "NN_BACKUP_ACK"
	case NNAddressCheckRequest:
		return "NN_ADDRESS_CHECK"
	case NNAddressCheckReply:
		return "NN_ADDRESS_REPLY"
	case NNNatifyRequest:
		return "NN_NATIFY_REQUEST"
	case NNReportRequest:
		return "NN_REPORT"
	case NNReportReply:
		return "NN_REPORT_ACK"
	case NNPreInitRequest:
		return "NN_PREINIT"
	case NNPreInitReply:
		return "NN_PREINIT_ACK"
	default:
		return fmt.Sprintf("UNKNOWN_0x%02x", command)
	}
}

func createPacketHeader(version byte, command byte, cookie uint32) []byte {
	header := []byte{0xfd, 0xfc, 0x1e, 0x66, 0x6a, 0xb2, version, command}
	return binary.BigEndian.AppendUint32(header, cookie)
}
