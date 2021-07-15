// Copyright 2014 Quoc-Viet Nguyen. All rights reserved.
// This software may be modified and distributed under the terms
// of the BSD license. See the LICENSE file for details.

package modbus

import (
	"encoding/binary"
	"fmt"
	"log"
	"sync"
	"time"
)

// RTUPassivityClientHandler implements Packager and Transporter interface.
type RTUPassivityClientHandler struct {
	rtuPassivityPackager
	rtuPassivitySerialTransporter
}

func NewRTUPassivityClientHandler(address string) *RTUPassivityClientHandler {
	handler := &RTUPassivityClientHandler{}
	handler.Address = address
	handler.Timeout = serialTimeout
	handler.IdleTimeout = serialIdleTimeout
	return handler
}

var dataQueue = make([]byte, 0, 128)
var sig = sync.NewCond(&sync.Mutex{})
var WriteChan = make(chan *NewRequest, 16)
var ReadChan = make(chan []byte, 1)

var l = &sync.Mutex{}

// NewClient creates a new modbus client with given backend handler.
func (mb *RTUPassivityClientHandler) NewClient(slaveId byte) Client {
	return &passivityClient{packager: mb, transporter: mb, SlaveId: slaveId}
}

func (mb *RTUPassivityClientHandler) Connect() {
	err := mb.rtuPassivitySerialTransporter.Connect()
	if err != nil {
		panic(err)
	}
	go mb.Listen()
}

// 循环读取
func (mb *RTUPassivityClientHandler) Listen() {
	go mb.startHandleLoop()
	go mb.startReadLoop()

	for {
		select {
		case newRequest := <-WriteChan:
			l.Lock()
			// write
			_ = mb.Close()
			if err := mb.connect(); err != nil {
				mb.logf("连接出错: %v", err)
			}
			// rs485电气特性，这里睡眠5ms
			time.Sleep(time.Millisecond * 5)
			mb.logf("modbus: sending % x\n", newRequest.Adu)
			if _, err := mb.port.Write(newRequest.Adu); err != nil {
				mb.logf("请求错误, %v, SlaveId: %v, funCode: %v", err, newRequest.Guide>>8, newRequest.Guide|0x00ff)
			}
			l.Unlock()
		case data := <-ReadChan:
			dataQueue = append(dataQueue, data...)
			sig.Signal()

		}
	}
}

// 循环处理
func (mb *RTUPassivityClientHandler) startHandleLoop() {
	for {
		for len(dataQueue) < 4 {
			mb.logf("小于4，进入睡眠，长度: %v", len(dataQueue))
			sig.L.Lock()
			sig.Wait()
			sig.L.Unlock()
			mb.logf("醒来")
		}
		mb.logf("本次queue: %v", dataQueue)

		// 遍历请求队列
		var hitGuide uint16
		requestSet.Range(func(key, value interface{}) bool {
			guide, newRequest := key.(uint16), value.(*NewRequest)
			mb.logf("本次主动事件: %v", requestSet)
			if binary.BigEndian.Uint16(dataQueue) == guide {
				mb.logf("命中主动请求事件: guide: %v", guide)
				expectedLength := newRequest.ExpectedLength()
				newRequest.Recv(dataQueue[:expectedLength])
				dataQueue = dataQueue[expectedLength:]
				hitGuide = guide
				return false
			}
			return true
		})

		// 如果不是主动请求，则匹配提前注册好的侦听回调函数
		if hitGuide == 0 {
			for guide, receiver := range receivers {
				if len(dataQueue) >= 4 && binary.BigEndian.Uint16(dataQueue) == guide {
					mb.logf("命中注册事件: guide: %v", guide)
					expectedLength := receiver.ExLen
					go receiver.Callback(dataQueue[:expectedLength])
					dataQueue = dataQueue[expectedLength:]
					hitGuide = 0xffff
					break
				}
			}
		}

		if hitGuide != 0 {
			requestSet.Delete(hitGuide)
		} else {
			// 如果没有匹配的Receiver或者请求，则去除头帧，继续遍历
			dataQueue = dataQueue[1:]
		}
	}
}

// 循环读取
func (mb *RTUPassivityClientHandler) startReadLoop() {
	for {
		l.Lock()
		if err := mb.connect(); err != nil {
			log.Fatalf("连接错误: %v", err)
		}
		l.Unlock()

		buf := make([]byte, 256)

		n, err := mb.port.Read(buf)
		if err != nil {
			mb.logf("读取错误: %v", err)
			time.Sleep(time.Millisecond * 500)
		}
		ReadChan <- buf[:n]
	}
}

// rtuPackager implements Packager interface.
type rtuPassivityPackager struct {
}

// Encode encodes PDU in a RTU frame:
//  Slave Address   : 1 byte
//  Function        : 1 byte
//  Data            : 0 up to 252 bytes
//  CRC             : 2 byte
func (mb *rtuPassivityPackager) Encode(pdu *ProtocolDataUnit) (adu []byte, err error) {
	length := len(pdu.Data) + 4
	if length > rtuMaxSize {
		err = fmt.Errorf("modbus: length of data '%v' must not be bigger than '%v'", length, rtuMaxSize)
		return
	}
	adu = make([]byte, length)

	adu[0] = pdu.SlaveId
	adu[1] = pdu.FunctionCode
	copy(adu[2:], pdu.Data)

	// Append crc
	var crc crc
	crc.reset().pushBytes(adu[0 : length-2])
	checksum := crc.value()

	adu[length-1] = byte(checksum >> 8)
	adu[length-2] = byte(checksum)
	return
}

// Verify verifies response length and slave id.
func (mb *rtuPassivityPackager) Verify(aduRequest []byte, aduResponse []byte) (err error) {
	length := len(aduResponse)
	// Minimum size (including address, function and CRC)
	if length < rtuMinSize {
		err = fmt.Errorf("modbus: response length '%v' does not meet minimum '%v'", length, rtuMinSize)
		return
	}
	// Slave address must match
	if aduResponse[0] != aduRequest[0] {
		err = fmt.Errorf("modbus: response slave id '%v' does not match request '%v'", aduResponse[0], aduRequest[0])
		return
	}
	return
}

// Decode extracts PDU from RTU frame and verify CRC.
func (mb *rtuPassivityPackager) Decode(adu []byte) (pdu *ProtocolDataUnit, err error) {
	length := len(adu)
	// Calculate checksum
	var crc crc
	crc.reset().pushBytes(adu[0 : length-2])
	checksum := uint16(adu[length-1])<<8 | uint16(adu[length-2])
	if checksum != crc.value() {
		//err = fmt.Errorf("modbus: response crc '%v' does not match expected '%v'", checksum, crc.value())
		fmt.Printf("modbus: response crc '%v' does not match expected '%v'\n 丢弃", checksum, crc.value())
		return
	}
	// Function code & data
	pdu = &ProtocolDataUnit{}
	pdu.FunctionCode = adu[1]
	pdu.Data = adu[2 : length-2]
	return
}

// rtuSerialTransporter implements Transporter interface.
type rtuPassivitySerialTransporter struct {
	serialPort
}

func (mb *rtuPassivitySerialTransporter) Send(aduRequest []byte) (aduResponse []byte, err error) {
	// Make sure port is connected
	if err = mb.serialPort.connect(); err != nil {
		return
	}
	// Start the timer to close when idle
	mb.serialPort.lastActivity = time.Now()
	//mb.serialPort.startCloseTimer()

	guide := binary.BigEndian.Uint16(aduRequest)
	r := mb.w(&NewRequest{Guide: guide, Adu: aduRequest})

	select {
	case <-time.NewTicker(time.Second * 5).C:
		RemoveRequest(guide)
		err = fmt.Errorf("响应超时, slaveId: %v, funCode: %v", aduRequest[0], aduRequest[1])
	case aduResponse = <-r:
		mb.serialPort.logf("modbus: received % x\n", aduResponse)
	}
	return
}

func (mb *rtuPassivitySerialTransporter) w(newRequest *NewRequest) chan []byte {
	WriteChan <- newRequest
	return RegisterRequest(newRequest)
}

type NewRequest struct {
	Guide uint16
	Adu   []byte
	resp  chan []byte
	Sent  bool
}

func (nr NewRequest) ExpectedLength() int {
	return calculateResponseLength(nr.Adu)
}

func (nr *NewRequest) Recv(resp []byte) {
	nr.resp <- resp
}

type Receiver struct {
	ExLen    byte
	Callback func([]byte)
}

var requestSet = new(sync.Map) // make(map[uint16]*NewRequest, 16)
var receivers = make(map[uint16]*Receiver, 16)

func RegisterReceiver(slaveId byte, funCode byte, rh *Receiver) {
	receivers[(uint16(slaveId)<<8)+uint16(funCode)] = rh
}

func RegisterRequest(newRequest *NewRequest) chan []byte {
	resp := make(chan []byte, 1)
	newRequest.resp = resp
	requestSet.Store(newRequest.Guide, newRequest)
	return resp
}

func RemoveRequest(guide uint16) {
	requestSet.Delete(guide)
}
