package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"github.com/goburrow/serial"
	"github.com/ya0yy/modbus"
	"io"
	"log"
	"sync"
	"time"
)

var jidianqi = make(chan uint16, 10)

// todo 需要实时更新状态，否则每次收到传感器数据会多次更新继电器

func main() {
	// Modbus RTU/ASCII
	handler := modbus.NewRTUPassivityClientHandler("/dev/tty.usbserial-14320")
	handler.BaudRate = 9600
	handler.DataBits = 8
	handler.StopBits = 1
	handler.Parity = "N"
	handler.Timeout = 50 * time.Minute
	handler.Logger = log.Default()

	handler.Connect()

	defer handler.Close()
	relayClient := modbus.NewNewClient(handler, 0x04)

	rh := &modbus.Receiver{
		ExLen: 8,
		Callback: func(resp []byte) {
			ret := (uint16(resp[4]) << 8) + uint16(resp[5])
			//jidianqi <- struct{}{}
			fmt.Printf("人体感应器: %v\n", ret)

		},
	}

	rh1 := &modbus.Receiver{
		ExLen: 10,
		Callback: func(resp []byte) {
			registerCode := binary.BigEndian.Uint16(resp[2:])
			if registerCode != 0x03 {
				fmt.Printf("忽略无用帧: %v \n", resp)
				return
			}
			ret := binary.BigEndian.Uint32(resp[4:8])
			fmt.Printf("光感: %v\n", ret)
			if ret < 10 {
				jidianqi <- 0
				log.Printf("照度: %v, 打开继电器", ret)
			} else {
				jidianqi <- 0xff00
			}
		},
	}

	modbus.RegisterReceiver(0x02, 6, rh)
	modbus.RegisterReceiver(0x03, 6, rh1)
	go switchInit()

	for cmd := range jidianqi {
		log.Println("进入发送")
		coil, err := relayClient.WriteSingleCoil(1, cmd)
		log.Println("继电器: ", coil)
		if err != nil {
			fmt.Printf("修改继电器出错了: %v \n", err)
		}
	}
	<-context.Background().Done()
}

var one = sync.Once{}

func switchInit() {
	go func() {
		ticker := time.NewTicker(time.Second * 3)
		for range ticker.C {
			one = sync.Once{}
		}
	}()

	cfg := &serial.Config{Address: "/dev/tty.usbserial-14340", BaudRate: 9600}

	iorwc, err := serial.Open(cfg)
	if err != nil {
		fmt.Println(err)
		return
	}

	defer iorwc.Close()
	buffer := make([]byte, 512)

	for {
		time.Sleep(time.Second * 1)

		n, err := io.ReadAtLeast(iorwc, buffer, 7)
		//num, err := iorwc.Read(buffer)
		if err != nil {
			fmt.Println("读取出错: ", err)
		}
		fmt.Printf("开关: % x\n", buffer[:n])
		one.Do(func() {
			jidianqi <- 0x5500
		})
	}
}
