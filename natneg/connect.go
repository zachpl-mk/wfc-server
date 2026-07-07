package natneg

import (
	"encoding/binary"
	"net"
	"strings"
	"time"
	"wwfc/common"
	"wwfc/logging"
	"wwfc/qr2"

	"github.com/logrusorgru/aurora/v3"
)

func (session *NATNEGSession) sendConnectRequests(moduleName string) {
	if session.Pairs == nil {
		session.Pairs = map[uint16]*NATNEGPair{}
	}

	for id, sender := range session.Clients {
		if !sender.isMapped() || sender.ConnectingIndex != id {
			continue
		}

		for destID, destination := range session.Clients {
			if id == destID || id > destID || !destination.isMapped() || destination.ConnectingIndex != destID {
				continue
			}

			key := pairKey(id, destID)
			if _, exists := session.Pairs[key]; exists {
				continue
			}

			pair := &NATNEGPair{
				A:         id,
				B:         destID,
				StartTime: time.Now(),
				Ack:       map[byte]bool{},
				Result:    map[byte]byte{},
			}
			session.Pairs[key] = pair
			sender.PairResults[destID] = 0
			destination.PairResults[id] = 0
			sender.ConnectingIndex = destID
			destination.ConnectingIndex = id

			logging.Notice(moduleName, "Starting peer negotiation", aurora.BrightCyan(id), "<->", aurora.BrightCyan(destID), "public", aurora.BrightCyan(sender.ServerIP), "<->", aurora.BrightCyan(destination.ServerIP))
			go session.runConnectPair(key)
		}
	}
}

func (session *NATNEGSession) runConnectPair(key uint16) {
	for {
		session.mutex.Lock()
		if !session.Open {
			session.mutex.Unlock()
			return
		}

		pair := session.Pairs[key]
		if pair == nil || pair.Reported {
			session.mutex.Unlock()
			return
		}

		a := session.Clients[pair.A]
		b := session.Clients[pair.B]
		if a == nil || b == nil {
			session.mutex.Unlock()
			return
		}

		now := time.Now()
		timedOut := now.Sub(pair.StartTime) >= ConnectPairDeadline || pair.RetryCount >= ConnectRetryLimit
		if timedOut {
			if !pair.TimeoutLogged {
				logging.Warn("NATNEG:"+formatCookie(session.Cookie), "Peer negotiation timed out", aurora.Cyan(pair.A), "<->", aurora.Cyan(pair.B), "retries", aurora.Cyan(pair.RetryCount))
				pair.TimeoutLogged = true
			}
			session.finishPairLocked(pair, 0x06, "timeout")
			session.mutex.Unlock()
			return
		}

		pair.RetryCount++
		pair.LastSend = now
		sendA := !pair.Ack[pair.B]
		sendB := !pair.Ack[pair.A]
		version := session.Version
		session.mutex.Unlock()

		if sendA {
			a.sendConnectRequestPackets(natnegConn, b, version, pair.RetryCount)
		}
		if sendB {
			b.sendConnectRequestPackets(natnegConn, a, version, pair.RetryCount)
		}

		time.Sleep(connectRetryDelay(pair.RetryCount))
	}
}

func connectRetryDelay(retry int) time.Duration {
	if retry <= 12 {
		return ConnectFastRetryDelay
	}
	return ConnectRetryDelay
}

func connectBurstCount(retry int) int {
	if retry == 1 {
		return ConnectInitialBurst
	}
	return 1
}

func (client *NATNEGClient) sendConnectRequestPackets(conn net.PacketConn, destination *NATNEGClient, version byte, retry int) {
	for i := 0; i < connectBurstCount(retry); i++ {
		client.sendConnectRequestPacket(conn, destination, version, retry)
	}
}

func (client *NATNEGClient) sendConnectRequestPacket(conn net.PacketConn, destination *NATNEGClient, version byte, retry int) {
	connectHeader := createPacketHeader(version, NNConnectRequest, destination.Cookie)
	endpoint := client.connectEndpointFor(destination)
	connectHeader = append(connectHeader, common.IPFormatBytes(endpoint)...)
	_, port := common.IPFormatToInt(endpoint)
	connectHeader = binary.BigEndian.AppendUint16(connectHeader, port)
	// GameSpy names these bytes "gotyourdata" and "finished". Keep the
	// existing values for MKWii compatibility.
	connectHeader = append(connectHeader, 0x42, 0x00)

	destIPAddr, err := net.ResolveUDPAddr("udp", destination.NegotiateIP)
	if err != nil {
		logging.Error("NATNEG", "Unable to resolve NATNEG endpoint", aurora.Cyan(destination.NegotiateIP), err.Error())
		return
	}

	_, err = conn.WriteTo(connectHeader, destIPAddr)
	if err != nil {
		logging.Error("NATNEG", "Failed to send connect request", aurora.Cyan(client.Index), "->", aurora.Cyan(destination.Index), "retry", aurora.Cyan(retry), err.Error())
	}
}

func (client *NATNEGClient) connectEndpointFor(destination *NATNEGClient) string {
	if client.LocalIP == "" || destination.LocalIP == "" {
		return client.ServerIP
	}
	if endpointHost(client.ServerIP) != endpointHost(destination.ServerIP) {
		return client.ServerIP
	}
	if !samePrivateLAN(client.LocalIP, destination.LocalIP) {
		return client.ServerIP
	}
	return client.LocalIP
}

func endpointHost(endpoint string) string {
	if strings.Contains(endpoint, ":") {
		return strings.Split(endpoint, ":")[0]
	}
	return endpoint
}

func samePrivateLAN(a, b string) bool {
	aIP := common.IPFormatBytes(a)
	bIP := common.IPFormatBytes(b)
	if len(aIP) != 4 || len(bIP) != 4 {
		return false
	}
	if !common.IsReservedIP(common.IPFormatNoPortToInt(endpointHost(a))) || !common.IsReservedIP(common.IPFormatNoPortToInt(endpointHost(b))) {
		return false
	}
	return aIP[0] == bIP[0] && aIP[1] == bIP[1] && aIP[2] == bIP[2]
}

func (session *NATNEGSession) handleConnectReply(conn net.PacketConn, addr net.Addr, buffer []byte, moduleName string, version byte) {
	if len(buffer) < 2 {
		logging.Error(moduleName, "Invalid packet size")
		return
	}

	clientIndex := buffer[1]
	client, exists := session.Clients[clientIndex]
	if !exists {
		logging.Warn(moduleName, "CONNECT_ACK from unknown client", aurora.Cyan(clientIndex))
		return
	}

	client.ConnectAck = true
	client.LastSeen = time.Now()

	peerIndex := client.ConnectingIndex
	if len(buffer) >= 3 && buffer[2] < 0x10 {
		peerIndex = buffer[2]
	}

	if pair := session.Pairs[pairKey(clientIndex, peerIndex)]; pair != nil {
		pair.Ack[clientIndex] = true
	}

	logging.Info(moduleName, "CONNECT_ACK client", aurora.Cyan(clientIndex), "peer", aurora.Cyan(peerIndex), "from", aurora.BrightCyan(addr.String()))
}

func (session *NATNEGSession) finishPairLocked(pair *NATNEGPair, result byte, reason string) {
	a := session.Clients[pair.A]
	b := session.Clients[pair.B]
	if a == nil || b == nil || pair.Reported {
		return
	}

	pair.Reported = true
	a.PairResults[pair.B] = result
	b.PairResults[pair.A] = result
	a.Result[pair.B] = result
	b.Result[pair.A] = result
	a.ConnectingIndex = a.Index
	b.ConnectingIndex = b.Index
	a.ConnectAck = false
	b.ConnectAck = false

	logging.Notice("NATNEG:"+formatCookie(session.Cookie), "Final pair result", aurora.Cyan(pair.A), "<->", aurora.Cyan(pair.B), "result", aurora.Cyan(result), "reason", aurora.Cyan(reason), "retries", aurora.Cyan(pair.RetryCount))
	qr2.ProcessNATNEGReport(result, a.ServerIP, b.ServerIP)
	session.sendConnectRequests("NATNEG:" + formatCookie(session.Cookie))
}
