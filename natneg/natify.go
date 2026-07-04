package natneg

import (
	"encoding/binary"
	"net"
	"strconv"
	"wwfc/logging"

	"github.com/logrusorgru/aurora/v3"
)

func handleERTTest(conn net.PacketConn, addr net.Addr, buffer []byte, moduleName string, version byte, cookie uint32) {
	if len(buffer) < 1 {
		logging.Error(moduleName, "Invalid packet size")
		return
	}

	portType := buffer[0]
	if portType > PortTypeNATNEG3 {
		logging.Error(moduleName, "Invalid port type")
		return
	}

	packet := createPacketHeader(version, NNErtTestRequest, cookie)
	packet = append(packet, portType)
	packet = appendObservedAddress(packet, addr)

	if _, err := conn.WriteTo(packet, addr); err != nil {
		logging.Warn(moduleName, "Failed to send ERT test reply:", aurora.Cyan(err))
	}
}

func handleAddressCheck(conn net.PacketConn, addr net.Addr, moduleName string, version byte, cookie uint32) {
	packet := createPacketHeader(version, NNAddressCheckReply, cookie)
	packet = appendObservedAddress(packet, addr)
	packet = append(packet, 0)

	if _, err := conn.WriteTo(packet, addr); err != nil {
		logging.Warn(moduleName, "Failed to send address check reply:", aurora.Cyan(err))
	}
}

func appendObservedAddress(packet []byte, addr net.Addr) []byte {
	host, port, err := net.SplitHostPort(addr.String())
	if err != nil {
		return append(packet, 0, 0, 0, 0, 0, 0, 0, 0)
	}

	ip := net.ParseIP(host).To4()
	if ip == nil {
		return append(packet, 0, 0, 0, 0, 0, 0, 0, 0)
	}

	portValue, err := strconv.Atoi(port)
	if err != nil {
		portValue = 0
	}

	packet = append(packet, 0, 2)
	packet = binary.BigEndian.AppendUint16(packet, uint16(portValue))
	packet = append(packet, ip...)
	return packet
}
