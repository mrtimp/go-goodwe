package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jessevdk/go-flags"
)

type GoodWeStatus int

const (
	WAITING GoodWeStatus = iota
	NORMAL
	ERROR
	CHECKING
)

type Data struct {
	Sample       time.Time    `json:"sample"`
	VoltageDC    [4]float64   `json:"voltage_dc"`
	CurrentDC    [4]float64   `json:"current_dc"`
	PowerDC      [4]float64   `json:"power_dc"`
	VoltageAC    [3]float64   `json:"voltage_ac"`
	CurrentAC    [3]float64   `json:"current_ac"`
	FrequencyAC  [3]float64   `json:"frequency_ac"`
	PowerAC      float64      `json:"power_ac"`
	Status       GoodWeStatus `json:"status"`
	Temperature  float64      `json:"temperature"`
	YieldToday   float64      `json:"yield_today"`
	YieldTotal   float64      `json:"yield_total"`
	WorkingHours float64      `json:"working_hours"`
}

type Client struct {
	Addr string
}

type Config struct {
	APIKey   string
	SystemID string
}

type Reading struct {
	Date        time.Time // will be formatted YYYYMMDD
	Power       int       // watts
	Energy      int       // watt-hours
	Voltage     int       // volts (optional)
	Temperature int       // degrees Celsius (optional)
}

type Options struct {
	ApiKey    string `short:"a" long:"api-key" description:"The PVOutput API key" env:"API_KEY" required:"true"`
	Debug     bool   `short:"d" long:"debug" description:"Show debug output"`
	IpAddress string `short:"i" long:"ip-address" description:"The IP address of the GoodWe inverter" env:"IP_ADDRESS" required:"true"`
	Port      int    `short:"p" long:"port" description:"The port that the GoodWe inverter is listening on" default:"8899" env:"PORT"`
	SystemID  string `short:"s" long:"system-id" description:"The PVOutput System ID" env:"SYSTEM_ID" required:"true"`
}

var opts Options

func main() {
	_, err := flags.Parse(&opts)

	if err != nil {
		os.Exit(1)
	}

	if opts.IpAddress == "" {
		log.Fatal("You must provide an IP address")
	}

	client := New(opts.IpAddress, opts.Port)

	for {
		data, err := client.GetData(3)
		if err != nil {
			log.Fatalf("Failed to get data: %v\n", err)
		}

		jsonData, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			log.Fatalf("Failed to marshal data: %v\n", err)
		}

		if opts.Debug {
			fmt.Println(string(jsonData))
			os.Exit(0)
		}

		cfg := Config{
			APIKey:   opts.ApiKey,
			SystemID: opts.SystemID,
		}

		reading := Reading{
			Date:        time.Now(),
			Power:       int(data.PowerAC),
			Energy:      int(data.YieldToday * 1000), // kWh → Wh
			Voltage:     int(data.VoltageAC[0]),
			Temperature: int(data.Temperature),
		}

		err = upload(cfg, reading)
		if err != nil {
			log.Fatalf("Upload to PVOutput failed: %v", err)
		}

		os.Exit(0)
	}
}

func New(ip string, port int) *Client {
	return &Client{Addr: fmt.Sprintf("%s:%d", ip, port)}
}

func (c *Client) GetData(retries int) (*Data, error) {
	for i := 0; i < retries; i++ {
		data, err := c.getData()
		if err == nil {
			return data, nil
		}
		if i < retries-1 {
			time.Sleep(time.Second)
		}
	}
	return nil, errors.New("failed to get data after retries")
}

func (c *Client) getData() (*Data, error) {
	conn, err := net.DialTimeout("udp", c.Addr, time.Second)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(1 * time.Second))

	// Discovery request payload
	request := []byte{0x7f, 0x03, 0x75, 0x94, 0x00, 0x49}
	request = append(request, CRC16(request)...)

	_, err = conn.Write(request)
	if err != nil {
		return nil, err
	}

	buf := make([]byte, 153)
	n, err := conn.Read(buf)
	if err != nil || n != 153 {
		return nil, fmt.Errorf("bad response size: got %d, want 153", n)
	}

	if !bytes.Equal(buf[:2], []byte{0xAA, 0x55}) {
		return nil, fmt.Errorf("invalid header: %x", buf[:2])
	}

	payload := buf[2:151]
	if !bytes.Equal(CRC16(payload), buf[151:]) {
		return nil, errors.New("CRC mismatch")
	}

	return parsePayload(payload)
}

func parsePayload(data []byte) (*Data, error) {
	d := &Data{Sample: time.Now()}

	// DC inputs
	for i := 0; i < 4; i++ {
		vi := 9 + i*4
		d.VoltageDC[i] = Parse16(data[vi:vi+2], -1)
		d.CurrentDC[i] = Parse16(data[vi+2:vi+4], -1)
		d.PowerDC[i] = d.VoltageDC[i] * d.CurrentDC[i]
	}

	// AC outputs
	for i := 0; i < 3; i++ {
		vi := 39 + i*2
		ci := 45 + i*2
		fi := 51 + i*2

		v := Parse16(data[vi:vi+2], -1)
		c := Parse16(data[ci:ci+2], -1)
		f := Parse16(data[fi:fi+2], -2)

		if i > 0 && v == 6553.5 {
			v, c, f = 0, 0, 0
		}

		d.VoltageAC[i] = v
		d.CurrentAC[i] = c
		d.FrequencyAC[i] = f
	}

	d.PowerAC = Parse16(data[59:61], 0)
	d.Status = GoodWeStatus(int(Parse16(data[61:63], 0)))
	d.Temperature = Parse16(data[85:87], -1)
	d.YieldToday = Parse16(data[91:93], -1)
	d.YieldTotal = Parse32(data[93:97], 0)
	d.WorkingHours = Parse16(data[99:101], 0)

	if d.YieldToday > 6500 || d.YieldTotal > 4_000_000 {
		return nil, errors.New("unrealistic yield values")
	}

	return d, nil
}

func Parse16(b []byte, exp int) float64 {
	return round(float64(binary.BigEndian.Uint16(b))*pow10(exp), -exp)
}

func Parse32(b []byte, exp int) float64 {
	return round(float64(binary.BigEndian.Uint32(b))*pow10(exp), -exp)
}

func pow10(exp int) float64 {
	switch {
	case exp == 0:
		return 1
	case exp > 0:
		return float64(int64(10) ^ int64(exp))
	default:
		v := 1.0
		for i := 0; i < -exp; i++ {
			v /= 10
		}
		return v
	}
}

func upload(cfg Config, r Reading) error {
	form := url.Values{}
	form.Set("d", r.Date.Format("20060102"))
	form.Set("t", r.Date.Format("15:04"))
	form.Set("v1", fmt.Sprintf("%d", r.Energy))
	form.Set("v2", fmt.Sprintf("%d", r.Power))
	if r.Voltage > 0 {
		form.Set("v6", fmt.Sprintf("%d", r.Voltage))
	}
	if r.Temperature > 0 {
		form.Set("v5", fmt.Sprintf("%d", r.Temperature))
	}

	req, err := http.NewRequest("POST", "https://pvoutput.org/service/r2/addstatus.jsp", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("X-Pvoutput-Apikey", cfg.APIKey)
	req.Header.Set("X-Pvoutput-SystemId", cfg.SystemID)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("upload failed: %s", resp.Status)
	}

	return nil
}

func round(f float64, places int) float64 {
	scale := pow10(places)
	return float64(int64(f*scale+0.5)) / scale
}

func CRC16(data []byte) []byte {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc ^= uint16(b)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0xA001
			} else {
				crc >>= 1
			}
		}
	}
	return []byte{byte(crc), byte(crc >> 8)}
}
