package natneg

import (
	"fmt"
	"net"
	"wwfc/logging"
	"wwfc/qr2"

	"github.com/logrusorgru/aurora/v3"
)

func (session *NATNEGSession) handleReport(conn net.PacketConn, addr net.Addr, buffer []byte, _moduleName string, version byte) {
	if len(buffer) < 9 {
		logging.Error(_moduleName, "Invalid packet size")
		return
	}

	response := createPacketHeader(version, NNReportReply, session.Cookie)
	response = append(response, buffer[:9]...)
	response[14] = 0
	if _, err := conn.WriteTo(response, addr); err != nil {
		logging.Warn(_moduleName, "Failed to send report ack:", aurora.Cyan(err))
		return
	}

	// portType := buffer[0]
	clientIndex := buffer[1]
	result := buffer[2]
	// natType := buffer[3]
	// mappingScheme := buffer[7]
	// gameName, err := common.GetString(buffer[11:])

	moduleName := "NATNEG:" + fmt.Sprintf("%08x/", session.Cookie) + addr.String()
	logging.Notice(moduleName, "Report from", aurora.BrightCyan(clientIndex), "result:", aurora.Cyan(result))

	if client, exists := session.Clients[clientIndex]; exists {
		connectingIndex := client.ConnectingIndex
		client.Result[connectingIndex] = result
		connecting := session.Clients[connectingIndex]
		client.ConnectingIndex = clientIndex
		client.ConnectAck = false
		client.RetryActive = false

		if connecting != nil {
			connecting.RetryActive = false
		}

		if connecting != nil {
			if otherResult, hasResult := connecting.Result[clientIndex]; hasResult {
				if otherResult != 1 {
					result = otherResult
				}
				qr2.ProcessNATNEGReport(result, client.ServerIP, connecting.ServerIP)
			}
		}
	}

	// Send remaining requests
	session.sendConnectRequests(moduleName)
}
