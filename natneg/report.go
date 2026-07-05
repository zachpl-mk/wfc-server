package natneg

import (
	"fmt"
	"net"
	"time"
	"wwfc/logging"

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
	conn.WriteTo(response, addr)

	// portType := buffer[0]
	clientIndex := buffer[1]
	result := buffer[2]
	natType := buffer[3]
	mappingScheme := buffer[7]
	// gameName, err := common.GetString(buffer[11:])

	moduleName := "NATNEG:" + fmt.Sprintf("%08x/", session.Cookie) + addr.String()
	logging.Notice(moduleName, "Report from", aurora.BrightCyan(clientIndex), "result", aurora.Cyan(result), "nat", aurora.Cyan(getNATTypeName(natType)), "mapping", aurora.Cyan(getMappingSchemeName(mappingScheme)))

	if client, exists := session.Clients[clientIndex]; exists {
		client.NATType = natType
		client.MappingScheme = mappingScheme
		client.LastSeen = time.Now()

		peerIndex := client.ConnectingIndex
		key := pairKey(clientIndex, peerIndex)
		pair := session.Pairs[key]
		if pair == nil {
			client.Result[peerIndex] = result
			client.PairResults[peerIndex] = result
		} else {
			pair.Result[clientIndex] = result
			if otherResult, hasResult := pair.Result[peerIndex]; hasResult {
				finalResult := result
				if otherResult != 1 {
					finalResult = otherResult
				}
				session.finishPairLocked(pair, finalResult, "client-report")
			}
		}
	}

	// Send remaining requests
	session.sendConnectRequests(moduleName)
}
