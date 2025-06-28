package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pelletier/go-toml/v2"
)

var version = "1.0.0"

type Config struct {
	Listen struct {
		TCP string `toml:"tcp"`
		UDP string `toml:"udp"`
	} `toml:"listen"`
	Backend struct {
		TCP string `toml:"tcp"`
		UDP string `toml:"udp"`
	} `toml:"backend"`
	IdleTimeoutSeconds int `toml:"idle_timeout_seconds"`
}

var (
	activeTCP int64
	activeUDP int64
)

func loadConfig(path string) Config {
	cfg := Config{}
	cfg.Listen.TCP = ":25565"
	cfg.Listen.UDP = ":25565"
	cfg.Backend.TCP = "127.0.0.1:25565"
	cfg.Backend.UDP = "127.0.0.1:25565"
	cfg.IdleTimeoutSeconds = 300

	f, err := os.ReadFile(path)
	if err != nil {
		log.Printf("config %s not found, using defaults", path)
		return cfg
	}
	if err := toml.Unmarshal(f, &cfg); err != nil {
		log.Fatalf("parse config: %v", err)
	}
	return cfg
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	cfg := loadConfig("config.toml")
	idle := time.Duration(cfg.IdleTimeoutSeconds) * time.Second

	log.Printf("mcproxy %s starting; tcp=%s udp=%s backend=%s", version, cfg.Listen.TCP, cfg.Listen.UDP, cfg.Backend.TCP)

	go udpForward(cfg.Listen.UDP, cfg.Backend.UDP, idle)

	go console()

	ln, err := net.Listen("tcp", cfg.Listen.TCP)
	if err != nil {
		log.Fatalf("tcp listen: %v", err)
	}
	for {
		c, err := ln.Accept()
		if err != nil {
			log.Printf("accept: %v", err)
			continue
		}
		go handleTCP(c, cfg.Backend.TCP)
	}
}

func handleTCP(client net.Conn, backendAddr string) {
	atomic.AddInt64(&activeTCP, 1)
	defer func() {
		client.Close()
		atomic.AddInt64(&activeTCP, -1)
	}()

	backend, err := net.Dial("tcp", backendAddr)
	if err != nil {
		log.Printf("dial backend: %v", err)
		return
	}
	defer backend.Close()

	cliAddr := client.RemoteAddr().(*net.TCPAddr)
	locAddr := backend.LocalAddr().(*net.TCPAddr)

	hdr := fmt.Sprintf("PROXY TCP4 %s %s %d %d\r\n", cliAddr.IP.String(), locAddr.IP.String(), cliAddr.Port, locAddr.Port)
	if _, err = io.WriteString(backend, hdr); err != nil {
		log.Printf("write hdr: %v", err)
		return
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { io.Copy(backend, client); backend.SetDeadline(time.Now()); wg.Done() }()
	go func() { io.Copy(client, backend); client.SetDeadline(time.Now()); wg.Done() }()
	wg.Wait()
}

type assoc struct {
	cliAddr  *net.UDPAddr
	backend  *net.UDPConn
	lastSeen time.Time
}

func udpForward(listenAddr, backendAddr string, idle time.Duration) {
	pc, err := net.ListenPacket("udp", listenAddr)
	if err != nil {
		log.Fatalf("udp listen: %v", err)
	}
	defer pc.Close()

	backendUDP, err := net.ResolveUDPAddr("udp", backendAddr)
	if err != nil {
		log.Fatalf("resolve backend: %v", err)
	}

	assocs := make(map[string]*assoc)
	var mu sync.Mutex

	go func() {
		for {
			time.Sleep(time.Minute)
			mu.Lock()
			for k, v := range assocs {
				if time.Since(v.lastSeen) > idle {
					v.backend.Close()
					delete(assocs, k)
					atomic.AddInt64(&activeUDP, -1)
				}
			}
			mu.Unlock()
		}
	}()

	buf := make([]byte, 2048)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			log.Printf("udp read: %v", err)
			continue
		}
		key := addr.String()

		mu.Lock()
		a, ok := assocs[key]
		if !ok {
			bc, err := net.DialUDP("udp", nil, backendUDP)
			if err != nil {
				mu.Unlock()
				log.Printf("dial udp backend: %v", err)
				continue
			}
			a = &assoc{cliAddr: addr.(*net.UDPAddr), backend: bc, lastSeen: time.Now()}
			assocs[key] = a
			atomic.AddInt64(&activeUDP, 1)

			go func(ac *assoc) {
				b := make([]byte, 2048)
				for {
					m, err := ac.backend.Read(b)
					if err != nil {
						return
					}
					pc.WriteTo(b[:m], ac.cliAddr)
				}
			}(a)
		}
		a.lastSeen = time.Now()
		_, _ = a.backend.Write(buf[:n])
		mu.Unlock()
	}
}

func console() {
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		cmd := strings.TrimSpace(sc.Text())
		switch cmd {
		case "stats":
			log.Printf("stats: tcp=%d udp=%d", atomic.LoadInt64(&activeTCP), atomic.LoadInt64(&activeUDP))
		case "quit", "exit", "stop":
			log.Println("shutdown requested")
			os.Exit(0)
		default:
			log.Printf("unknown cmd: %s", cmd)
		}
	}
}
