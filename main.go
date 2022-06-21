package main

import (
	"encoding/json"
	"fmt"
	MQTT "github.com/eclipse/paho.mqtt.golang"
	"github.com/stianeikeland/go-rpio/v4"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"
)

type Wind struct {
	ID        float64 `json:"id"`
	Timestamp float64 `json:"timestamp"`
	Value     string  `json:"value"`
}

var w1Pin rpio.Pin

func main() {
	rpio.Open()
	defer rpio.Close()

	// Pins 18/19 were chosen intentionally because there are only 2 PWM channels on the Pi
	// and these pins are each on one of those separate channels
	w1Pin = rpio.Pin(19)
	w1Pin.Mode(rpio.Pwm)
	w1Pin.Freq(64000)
	w1Pin.DutyCycleWithPwmMode(0, 128, rpio.Balanced)

	tracker := newTracker()

	connOpts := MQTT.NewClientOptions().AddBroker("mq.edjusted.com:1883").SetClientID("analog-wind").SetCleanSession(true)
	connOpts.OnConnect = func(c MQTT.Client) {
		if token := c.Subscribe("/ws/4/ind/wind_speed", byte(0), tracker.onMessageReceived); token.Wait() && token.Error() != nil {
			panic(token.Error())
		}
	}

	client := MQTT.NewClient(connOpts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		panic(token.Error())
	} else {
		fmt.Printf("Connected to %s\n", "mq.edjusted.com")
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	<-c

}

type tracker struct {
	mtx    sync.RWMutex
	setVal uint32
	actVal uint32
}

func newTracker() *tracker {
	t := &tracker{
		setVal: 0,
		actVal: 0,
	}
	go func() { t.loop() }()
	return t
}

func (t *tracker) onMessageReceived(client MQTT.Client, message MQTT.Message) {
	var payload Wind
	err := json.Unmarshal(message.Payload(), &payload)
	if err != nil {
		fmt.Println("msg", "error parsing json", "err", err)
		return
	}
	spd, err := strconv.Atoi(payload.Value)
	if err != nil {
		fmt.Println("msg", "error parsing value to int", "val", payload.Value, "err", err)
		return
	}
	if spd > 32 {
		spd = 32
	}
	// The volt meter is 0-3V, the output voltage of the PMW at full (32/32) will be 3.3V
	// The cycleLen is 32, so we are really just close enough to just output the wind speed
	// as the duty cycle
	//w1Pin.DutyCycleWithPwmMode(uint32(spd), 32, rpio.Balanced)

	t.update(uint32(spd) * 4)

	fmt.Printf("Received message on topic: %s\nMessage: %s\n", message.Topic(), message.Payload())
}

func (t *tracker) update(set uint32) {
	t.mtx.Lock()
	t.setVal = set
	t.mtx.Unlock()
}

func (t *tracker) loop() {
	for {
		t.mtx.RLock()
		des := t.setVal
		t.mtx.RUnlock()
		if t.actVal < des {
			t.actVal++
			w1Pin.DutyCycleWithPwmMode(t.actVal, 128, rpio.Balanced)
		} else if t.actVal > des {
			t.actVal--
			w1Pin.DutyCycleWithPwmMode(t.actVal, 128, rpio.Balanced)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
