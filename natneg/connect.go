package natneg

import (
	"encoding/binary"
	"net"
	"time"
	"wwfc/common"
	"wwfc/logging"

	"github.com/logrusorgru/aurora/v3"
)

func (session *NATNEGSession) sendConnectRequests(moduleName string) {
	for id, sender := range session.Clients {
		if !sender.isMapped() || sender.ConnectingIndex != id {
			continue
		}

		for destID, destination := range session.Clients {
			if id == destID || !destination.isMapped() || destination.ConnectingIndex != destID {
				continue
			}

			if _, hasResult := destination.Result[id]; hasResult {
				continue
			}

			logging.Notice(moduleName, "Exchange connect requests between", aurora.BrightCyan(id), "and", aurora.BrightCyan(destID))
			sender.ConnectingIndex = destID
			sender.ConnectAck = false
			sender.RetryActive = true
			destination.ConnectingIndex = id
			destination.ConnectAck = false
			destination.RetryActive = true

			go session.retryConnectRequests(moduleName, sender.Index, destination.Index)
		}
	}
}

func (session *NATNEGSession) retryConnectRequests(moduleName string, indexA byte, indexB byte) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		session.mutex.Lock()
		if !session.Open {
			session.mutex.Unlock()
			return
		}

		clientA := session.Clients[indexA]
		clientB := session.Clients[indexB]
		if clientA == nil || clientB == nil {
			session.mutex.Unlock()
			return
		}

		sendToA := clientA.RetryActive && !clientA.ConnectAck && clientA.ConnectingIndex == clientB.Index
		sendToB := clientB.RetryActive && !clientB.ConnectAck && clientB.ConnectingIndex == clientA.Index
		if !sendToA && !sendToB {
			clientA.RetryActive = false
			clientB.RetryActive = false
			session.mutex.Unlock()
			return
		}

		version := session.Version
		session.mutex.Unlock()

		if sendToA {
			clientB.sendConnectRequestPacket(natnegConn, clientA, version, moduleName)
		}
		if sendToB {
			clientA.sendConnectRequestPacket(natnegConn, clientB, version, moduleName)
		}

		<-ticker.C
	}
}

func (client *NATNEGClient) sendConnectRequestPacket(conn net.PacketConn, destination *NATNEGClient, version byte, moduleName string) {
	connectHeader := createPacketHeader(version, NNConnectRequest, destination.Cookie)
	connectHeader = append(connectHeader, common.IPFormatBytes(client.ServerIP)...)
	_, port := common.IPFormatToInt(client.ServerIP)
	connectHeader = binary.BigEndian.AppendUint16(connectHeader, port)
	// Two bytes: "gotyourdata" and "finished"
	connectHeader = append(connectHeader, 0x42, 0x00)

	destIPAddr, err := net.ResolveUDPAddr("udp", destination.NegotiateIP)
	if err != nil {
		logging.Warn(moduleName, "Invalid negotiate address", aurora.Cyan(destination.NegotiateIP), aurora.Cyan(err))
		return
	}
	if _, err := conn.WriteTo(connectHeader, destIPAddr); err != nil {
		logging.Warn(moduleName, "Failed to send connect request:", aurora.Cyan(err))
	}
}

func (session *NATNEGSession) handleConnectReply(conn net.PacketConn, addr net.Addr, buffer []byte, moduleName string, version byte) {
	if len(buffer) < 2 {
		logging.Error(moduleName, "Invalid packet size")
		return
	}

	// portType := buffer[0]
	clientIndex := buffer[1]
	// useGamePort := buffer[2]
	// localIPBytes := buffer[3:7]

	if client, exists := session.Clients[clientIndex]; exists {
		client.ConnectAck = true
	}
}
