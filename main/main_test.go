package main

import (
	"context"
	"fmt"
	"github.com/ya0yy/modbus"
	"log"
	"testing"
	"time"
)

func TestOne(t *testing.T) {
	// Modbus RTU/ASCII
	handler := modbus.NewRTUPassivityClientHandler("COM3", context.TODO())
	handler.BaudRate = 9600
	handler.DataBits = 8
	handler.StopBits = 1
	handler.Parity = "N"
	handler.Timeout = 50 * time.Minute
	handler.Logger = log.Default()

	handler.Connect()

	defer handler.Close()

	//illuminanceClient := handler.NewClient(0x03)
	relayClient := handler.NewClient(0x02)

	rh := &modbus.Receiver{
		ExLen: 8,
		Handle: func(slaveId byte, funCode byte, resp []byte, _ context.Context) {
			//jidianqi <- struct{}{}
			ret := (uint16(resp[4]) << 8) + uint16(resp[5])
			// 02 06 00 02 00 01 e9 f9
			//n, err := motionClient.WriteSingleRegister(0x02, ret)
			//if err != nil {
			//	fmt.Printf("错误回复: %v \n", resp)
			//	return
			//}
			fmt.Printf("接收: %v\n", ret)
		},
	}

	handler.RegisterReceiver(0x02, 10, rh)

	relayClient.DirectWrite([]byte{0x55, 0x02, 0x10, 0, 0, 0, 0, 0x67})

}
