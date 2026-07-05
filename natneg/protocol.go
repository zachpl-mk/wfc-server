package natneg

import (
	"encoding/binary"
	"fmt"
	"net"
	"time"
	"wwfc/common"
	"wwfc/logging"

	"github.com/logrusorgru/aurora/v3"
)

func formatCookie(cookie uint32) string {
	return fmt.Sprintf("%08x", cookie)
}

func getNATTypeName(natType byte) string {
	switch natType {
	case NATTypeNoNat:
		return "NoNat"
	case NATTypeFirewallOnly:
		return "FirewallOnly"
	case NATTypeFullCone:
		return "FullCone"
	case NATTypeRestrictedCone:
		return "RestrictedCone"
	case NATTypePortRestrictedCone:
		return "PortRestrictedCone"
	case NATTypeSymmetric:
		return "Symmetric"
	case NATTypeUnknown:
		return "Unknown"
	default:
		return fmt.Sprintf("Unknown(0x%02x)", natType)
	}
}

func getMappingSchemeName(mapping byte) string {
	switch mapping {
	case NATMappingUnknown:
		return "Unknown"
	case NATMappingSamePrivatePublic:
		return "SamePrivatePublic"
	case NATMappingConsistent:
		return "Consistent"
	case NATMappingIncremental:
		return "Incremental"
	case NATMappingMixed:
		return "Mixed"
	default:
		return fmt.Sprintf("Unknown(0x%02x)", mapping)
	}
}

func handleAddressCheck(conn net.PacketConn, addr net.Addr, cookie uint32, version byte, buffer []byte, moduleName string) {
	udpAddr, ok := addr.(*net.UDPAddr)
	if !ok {
		logging.Error(moduleName, "ADDRESS_CHECK from non-UDP address", aurora.Cyan(addr.String()))
		return
	}

	ip := udpAddr.IP.To4()
	if ip == nil {
		logging.Error(moduleName, "ADDRESS_CHECK from non-IPv4 address", aurora.Cyan(addr.String()))
		return
	}

	response := createPacketHeader(version, NNAddressCheckReply, cookie)
	response = append(response, ip...)
	response = binary.BigEndian.AppendUint16(response, uint16(udpAddr.Port))
	if len(buffer) != 0 {
		// Several GameSpy variants echo trailing request data. Echoing is
		// harmless for MKWii and keeps the reply tolerant of SDK revisions.
		response = append(response, buffer...)
	}

	_, err := conn.WriteTo(response, addr)
	if err != nil {
		logging.Error(moduleName, "Failed ADDRESS_REPLY:", err.Error())
		return
	}

	logging.Notice(moduleName, "ADDRESS_REPLY public", aurora.BrightCyan(addr.String()))
}

func handleNatify(conn net.PacketConn, addr net.Addr, cookie uint32, version byte, buffer []byte, moduleName string) {
	response := createPacketHeader(version, NNNatifyRequest, cookie)
	if len(buffer) >= 2 {
		response = append(response, buffer[:2]...)
	}

	_, err := conn.WriteTo(response, addr)
	if err != nil {
		logging.Error(moduleName, "Failed NATIFY response:", err.Error())
		return
	}

	logging.Notice(moduleName, "NATIFY echo to", aurora.BrightCyan(addr.String()))
}

func (session *NATNEGSession) handleERTTest(conn net.PacketConn, addr net.Addr, buffer []byte, moduleName string, version byte) {
	response := createPacketHeader(version, NNErtTestReply, session.Cookie)
	response = append(response, buffer...)
	_, err := conn.WriteTo(response, addr)
	if err != nil {
		logging.Error(moduleName, "Failed ERT_ACK:", err.Error())
		return
	}
	logging.Info(moduleName, "ERT_TEST acked bytes", aurora.Cyan(len(buffer)))
}

func (session *NATNEGSession) handleBackupTest(conn net.PacketConn, addr net.Addr, buffer []byte, moduleName string, version byte) {
	response := createPacketHeader(version, NNBackupTestReply, session.Cookie)
	response = append(response, buffer...)
	_, err := conn.WriteTo(response, addr)
	if err != nil {
		logging.Error(moduleName, "Failed BACKUP_ACK:", err.Error())
		return
	}
	logging.Info(moduleName, "BACKUP_TEST acked bytes", aurora.Cyan(len(buffer)))
}

func (session *NATNEGSession) handleERTAck(addr net.Addr, buffer []byte, moduleName string) {
	logging.Info(moduleName, "ERT_ACK from", aurora.BrightCyan(addr.String()), "bytes", aurora.Cyan(len(buffer)))
}

func (session *NATNEGSession) handleBackupAck(addr net.Addr, buffer []byte, moduleName string) {
	logging.Info(moduleName, "BACKUP_ACK from", aurora.BrightCyan(addr.String()), "bytes", aurora.Cyan(len(buffer)))
}

func (session *NATNEGSession) handleStateUpdate(addr net.Addr, buffer []byte, moduleName string) {
	if len(buffer) < 2 {
		logging.Warn(moduleName, "Short STATE_UPDATE from", aurora.BrightCyan(addr.String()))
		return
	}

	clientIndex := buffer[1]
	if client := session.Clients[clientIndex]; client != nil {
		client.LastSeen = time.Now()
	}
	logging.Info(moduleName, "STATE_UPDATE client", aurora.Cyan(clientIndex), "bytes", aurora.Cyan(len(buffer)))
}

func (session *NATNEGSession) handleConnectPing(addr net.Addr, buffer []byte, moduleName string) {
	if len(buffer) < 2 {
		logging.Warn(moduleName, "Short CONNECT_PING from", aurora.BrightCyan(addr.String()))
		return
	}

	clientIndex := buffer[1]
	if client := session.Clients[clientIndex]; client != nil {
		client.LastSeen = time.Now()
	}
	logging.Info(moduleName, "CONNECT_PING client", aurora.Cyan(clientIndex), "bytes", aurora.Cyan(len(buffer)))
}

func sendReportCancel(version byte, cookie uint32, client *NATNEGClient) {
	if client.NegotiateIP == "" {
		return
	}

	reportAck := createPacketHeader(version, NNReportReply, cookie)
	reportAck = append(reportAck, 0x00, client.Index, 0x00)
	reportAck = append(reportAck, 0x00, 0x00, 0x00, NATTypeUnknown, NATMappingUnknown, 0x00)

	addr, err := net.ResolveUDPAddr("udp", client.NegotiateIP)
	if err != nil {
		logging.Error("NATNEG", "Unable to resolve cancel endpoint", aurora.Cyan(client.NegotiateIP), err.Error())
		return
	}

	_, err = natnegConn.WriteTo(reportAck, addr)
	if err != nil {
		logging.Error("NATNEG", "Failed report cancel to", aurora.Cyan(client.Index), err.Error())
	}
}

func endpointBytes(endpoint string) ([]byte, uint16) {
	ip := common.IPFormatBytes(endpoint)
	_, port := common.IPFormatToInt(endpoint)
	return ip, port
}
