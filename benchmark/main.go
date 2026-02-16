// BitTorrent UDP Tracker Benchmark Tool
// Simulates concurrent UDP clients to test tracker performance
//
// This tool creates multiple workers that each:
// 1. Connect to the tracker (get connection ID)
// 2. Send announce requests for multiple torrents
// 3. Send scrape requests
//
// Metrics measured:
// - RPS (Requests Per Second): Overall throughput
// - Latency (Min/Avg/P50/P95/P99/Max): Response time distribution
// - Error rate: Failed requests percentage
// - Memory growth: Memory usage during test
//
// Usage: go run benchmark/main.go -target localhost:1337 -duration 30s -concurrency 100

package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"net"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const (
	protocolID      = 0x41727101980
	actionConnect   = 0
	actionAnnounce  = 1
	actionScrape    = 2
	connectTimeout  = 5 * time.Second
	responseTimeout = 5 * time.Second
)

// Stats holds all benchmark metrics
// These are used to calculate RPS, latency percentiles, and error rates
type Stats struct {
	TotalRequests   uint64          // Total requests sent (connect + announce + scrape)
	SuccessfulReqs  uint64          // Requests that received valid responses
	FailedReqs      uint64          // Requests that timed out or errored
	ConnectCount    uint64          // Number of connect operations
	AnnounceCount   uint64          // Number of announce operations
	ScrapeCount     uint64          // Number of scrape operations
	Latencies       []time.Duration // All successful request latencies for percentile calculation
	LatenciesMu     sync.Mutex      // Protects Latencies slice
	StartTime       time.Time       // Test start time
	PeakMemoryMB    float64         // Highest memory usage seen
	InitialMemoryMB float64         // Memory usage at start
}

type Config struct {
	Target      string
	Duration    time.Duration
	Concurrency int
	RateLimit   int // requests per second per worker
	NumHashes   int // number of different info_hashes to use
	NumWant     int // peers to request
}

type Benchmark struct {
	Config Config
	Stats  Stats
	StopCh chan struct{}
}

func NewBenchmark(cfg Config) *Benchmark {
	return &Benchmark{
		Config: cfg,
		Stats: Stats{
			Latencies: make([]time.Duration, 0, 100000),
		},
		StopCh: make(chan struct{}),
	}
}

func (b *Benchmark) Run() {
	b.Stats.StartTime = time.Now()
	b.Stats.InitialMemoryMB = getMemoryMB()

	fmt.Printf("Starting benchmark...\n")
	fmt.Printf("Target: %s\n", b.Config.Target)
	fmt.Printf("Duration: %s\n", b.Config.Duration)
	fmt.Printf("Concurrency: %d\n", b.Config.Concurrency)
	fmt.Printf("Rate limit: %d req/s per worker\n", b.Config.RateLimit)
	fmt.Printf("Info hashes: %d\n", b.Config.NumHashes)
	fmt.Println()

	// Start memory monitoring
	go b.monitorMemory()

	// Start progress reporter
	go b.reportProgress()

	// Run workers
	var wg sync.WaitGroup
	for i := 0; i < b.Config.Concurrency; i++ {
		wg.Add(1)
		go b.worker(i, &wg)
	}

	// Wait for duration
	time.Sleep(b.Config.Duration)
	close(b.StopCh)

	wg.Wait()
	b.printResults()
}

func (b *Benchmark) worker(id int, wg *sync.WaitGroup) {
	defer wg.Done()

	// Create UDP connection
	conn, err := net.Dial("udp", b.Config.Target)
	if err != nil {
		log.Printf("Worker %d: failed to connect: %v", id, err)
		return
	}
	defer conn.Close()

	udpConn := conn.(*net.UDPConn)
	udpConn.SetDeadline(time.Now().Add(b.Config.Duration + 10*time.Second))

	// Rate limiter
	var rateLimiter <-chan time.Time
	if b.Config.RateLimit > 0 {
		rateLimiter = time.Tick(time.Second / time.Duration(b.Config.RateLimit))
	}

	// Pre-generate info hashes and peer IDs for this worker
	hashes := make([][20]byte, b.Config.NumHashes)
	peerID := generatePeerID(id)
	for i := range hashes {
		hashes[i] = generateInfoHash(id, i)
	}

	for {
		select {
		case <-b.StopCh:
			return
		default:
		}

		if rateLimiter != nil {
			<-rateLimiter
		}

		// Perform a full cycle: connect -> announce -> scrape
		b.performCycle(udpConn, peerID, hashes)
	}
}

func (b *Benchmark) performCycle(conn *net.UDPConn, peerID [20]byte, hashes [][20]byte) {
	start := time.Now()

	// Step 1: Connect
	connectionID, err := b.doConnect(conn)
	if err != nil {
		atomic.AddUint64(&b.Stats.FailedReqs, 1)
		atomic.AddUint64(&b.Stats.TotalRequests, 1)
		return
	}
	atomic.AddUint64(&b.Stats.ConnectCount, 1)
	atomic.AddUint64(&b.Stats.SuccessfulReqs, 1)
	atomic.AddUint64(&b.Stats.TotalRequests, 1)
	b.recordLatency(time.Since(start))

	// Step 2: Announce for each hash
	for _, hash := range hashes {
		select {
		case <-b.StopCh:
			return
		default:
		}

		start = time.Now()
		err = b.doAnnounce(conn, connectionID, hash, peerID)
		if err != nil {
			atomic.AddUint64(&b.Stats.FailedReqs, 1)
		} else {
			atomic.AddUint64(&b.Stats.AnnounceCount, 1)
			atomic.AddUint64(&b.Stats.SuccessfulReqs, 1)
			atomic.AddUint64(&b.Stats.TotalRequests, 1)
			b.recordLatency(time.Since(start))
		}
	}

	// Step 3: Scrape (for first hash only)
	start = time.Now()
	err = b.doScrape(conn, connectionID, hashes[0])
	if err != nil {
		atomic.AddUint64(&b.Stats.FailedReqs, 1)
	} else {
		atomic.AddUint64(&b.Stats.ScrapeCount, 1)
		atomic.AddUint64(&b.Stats.SuccessfulReqs, 1)
		atomic.AddUint64(&b.Stats.TotalRequests, 1)
		b.recordLatency(time.Since(start))
	}
}

func (b *Benchmark) doConnect(conn *net.UDPConn) (uint64, error) {
	transactionID := uint32(time.Now().UnixNano())

	request := make([]byte, 16)
	binary.BigEndian.PutUint64(request[0:8], protocolID)
	binary.BigEndian.PutUint32(request[8:12], actionConnect)
	binary.BigEndian.PutUint32(request[12:16], transactionID)

	if _, err := conn.Write(request); err != nil {
		return 0, err
	}

	response := make([]byte, 16)
	conn.SetReadDeadline(time.Now().Add(responseTimeout))
	n, err := conn.Read(response)
	if err != nil {
		return 0, err
	}

	if n < 16 {
		return 0, fmt.Errorf("short response")
	}

	respAction := binary.BigEndian.Uint32(response[0:4])
	respTxID := binary.BigEndian.Uint32(response[4:8])

	if respAction != actionConnect || respTxID != transactionID {
		return 0, fmt.Errorf("invalid response")
	}

	return binary.BigEndian.Uint64(response[8:16]), nil
}

func (b *Benchmark) doAnnounce(conn *net.UDPConn, connectionID uint64, infoHash, peerID [20]byte) error {
	transactionID := uint32(time.Now().UnixNano())

	request := make([]byte, 98)
	binary.BigEndian.PutUint64(request[0:8], connectionID)
	binary.BigEndian.PutUint32(request[8:12], actionAnnounce)
	binary.BigEndian.PutUint32(request[12:16], transactionID)
	copy(request[16:36], infoHash[:])
	copy(request[36:56], peerID[:])
	binary.BigEndian.PutUint64(request[56:64], 0)   // downloaded
	binary.BigEndian.PutUint64(request[64:72], 100) // left
	binary.BigEndian.PutUint64(request[72:80], 0)   // uploaded
	binary.BigEndian.PutUint32(request[80:84], 0)   // event (none)
	binary.BigEndian.PutUint32(request[84:88], 0)   // IP
	binary.BigEndian.PutUint32(request[88:92], 0)   // key
	binary.BigEndian.PutUint32(request[92:96], uint32(b.Config.NumWant))
	binary.BigEndian.PutUint16(request[96:98], 6881) // port

	if _, err := conn.Write(request); err != nil {
		return err
	}

	response := make([]byte, 1500)
	conn.SetReadDeadline(time.Now().Add(responseTimeout))
	n, err := conn.Read(response)
	if err != nil {
		return err
	}

	if n < 20 {
		return fmt.Errorf("short response")
	}

	respAction := binary.BigEndian.Uint32(response[0:4])
	respTxID := binary.BigEndian.Uint32(response[4:8])

	if respAction != actionAnnounce || respTxID != transactionID {
		return fmt.Errorf("invalid response")
	}

	return nil
}

func (b *Benchmark) doScrape(conn *net.UDPConn, connectionID uint64, infoHash [20]byte) error {
	transactionID := uint32(time.Now().UnixNano())

	request := make([]byte, 36)
	binary.BigEndian.PutUint64(request[0:8], connectionID)
	binary.BigEndian.PutUint32(request[8:12], actionScrape)
	binary.BigEndian.PutUint32(request[12:16], transactionID)
	copy(request[16:36], infoHash[:])

	if _, err := conn.Write(request); err != nil {
		return err
	}

	response := make([]byte, 20)
	conn.SetReadDeadline(time.Now().Add(responseTimeout))
	n, err := conn.Read(response)
	if err != nil {
		return err
	}

	if n < 20 {
		return fmt.Errorf("short response")
	}

	respAction := binary.BigEndian.Uint32(response[0:4])
	respTxID := binary.BigEndian.Uint32(response[4:8])

	if respAction != actionScrape || respTxID != transactionID {
		return fmt.Errorf("invalid response")
	}

	return nil
}

func (b *Benchmark) recordLatency(d time.Duration) {
	b.Stats.LatenciesMu.Lock()
	b.Stats.Latencies = append(b.Stats.Latencies, d)
	b.Stats.LatenciesMu.Unlock()
}

func (b *Benchmark) monitorMemory() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			mem := getMemoryMB()
			if mem > b.Stats.PeakMemoryMB {
				b.Stats.PeakMemoryMB = mem
			}
		case <-b.StopCh:
			return
		}
	}
}

func (b *Benchmark) reportProgress() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			elapsed := time.Since(b.Stats.StartTime)
			total := atomic.LoadUint64(&b.Stats.TotalRequests)
			rps := float64(total) / elapsed.Seconds()
			fmt.Printf("[%s] Total: %d | RPS: %.0f | Success: %d | Failed: %d\n",
				elapsed.Round(time.Second), total, rps,
				atomic.LoadUint64(&b.Stats.SuccessfulReqs),
				atomic.LoadUint64(&b.Stats.FailedReqs))
		case <-b.StopCh:
			return
		}
	}
}

// printResults outputs the final benchmark report
//
// How to interpret the results:
//
// Request Statistics:
// - Total Requests: Sum of all operations (connect + announce + scrape)
// - Successful: Should be >99% for good performance
// - Failed: Should be <1% (timeouts or errors)
// - RPS: Throughput - higher is better, should scale with concurrency
//
// Latency Statistics (measured for successful requests only):
// - Min: Fastest response (best case with warm caches)
// - Avg: Mean latency (can be skewed by outliers)
// - P50: Median - 50% of requests were faster than this
// - P95: 95% of requests were faster (important for user experience)
// - P99: 99% of requests were faster (catches worst cases)
// - Max: Slowest response (usually includes some timeouts)
//
// Good local latency targets:
// - P50 < 5ms, P95 < 10ms, P99 < 50ms
//
// Memory Usage:
// - Growth should be reasonable for the number of connections
// - Continuous growth throughout test may indicate a leak
func (b *Benchmark) printResults() {
	elapsed := time.Since(b.Stats.StartTime)

	fmt.Println()
	fmt.Println("========================================")
	fmt.Println("       BENCHMARK RESULTS")
	fmt.Println("========================================")
	fmt.Printf("Duration: %s\n", elapsed.Round(time.Millisecond))
	fmt.Printf("Concurrency: %d workers\n", b.Config.Concurrency)
	fmt.Println()

	fmt.Println("--- Request Statistics ---")
	fmt.Printf("Total Requests:     %d\n", b.Stats.TotalRequests)
	successRate := float64(0)
	if b.Stats.TotalRequests > 0 {
		successRate = float64(b.Stats.SuccessfulReqs) / float64(b.Stats.TotalRequests) * 100
	}
	fmt.Printf("Successful:         %d (%.2f%%)\n", b.Stats.SuccessfulReqs, successRate)
	failRate := float64(0)
	if b.Stats.TotalRequests > 0 {
		failRate = float64(b.Stats.FailedReqs) / float64(b.Stats.TotalRequests) * 100
	}
	fmt.Printf("Failed:             %d (%.2f%%)\n", b.Stats.FailedReqs, failRate)
	fmt.Printf("Requests/Second:    %.2f\n", float64(b.Stats.TotalRequests)/elapsed.Seconds())
	fmt.Println()

	fmt.Println("--- Request Breakdown ---")
	fmt.Printf("Connect:            %d\n", b.Stats.ConnectCount)
	fmt.Printf("Announce:           %d\n", b.Stats.AnnounceCount)
	fmt.Printf("Scrape:             %d\n", b.Stats.ScrapeCount)
	fmt.Println()

	fmt.Println("--- Latency Statistics ---")
	if len(b.Stats.Latencies) > 0 {
		b.Stats.LatenciesMu.Lock()
		sort.Slice(b.Stats.Latencies, func(i, j int) bool {
			return b.Stats.Latencies[i] < b.Stats.Latencies[j]
		})

		p50 := percentile(b.Stats.Latencies, 50)
		p95 := percentile(b.Stats.Latencies, 95)
		p99 := percentile(b.Stats.Latencies, 99)
		min := b.Stats.Latencies[0]
		max := b.Stats.Latencies[len(b.Stats.Latencies)-1]

		var sum time.Duration
		for _, l := range b.Stats.Latencies {
			sum += l
		}
		avg := sum / time.Duration(len(b.Stats.Latencies))

		b.Stats.LatenciesMu.Unlock()

		fmt.Printf("Min:                %s\n", min)
		fmt.Printf("Avg:                %s\n", avg)
		fmt.Printf("P50:                %s\n", p50)
		fmt.Printf("P95:                %s\n", p95)
		fmt.Printf("P99:                %s\n", p99)
		fmt.Printf("Max:                %s\n", max)
	}
	fmt.Println()

	fmt.Println("--- Memory Usage ---")
	fmt.Printf("Initial:            %.2f MB\n", b.Stats.InitialMemoryMB)
	fmt.Printf("Peak:               %.2f MB\n", b.Stats.PeakMemoryMB)
	fmt.Printf("Growth:             %.2f MB\n", b.Stats.PeakMemoryMB-b.Stats.InitialMemoryMB)
	fmt.Println("========================================")
	fmt.Println()

	// Quick interpretation hints
	if b.Stats.TotalRequests > 0 {
		successRate := float64(b.Stats.SuccessfulReqs) / float64(b.Stats.TotalRequests) * 100
		if successRate < 95 {
			fmt.Println("⚠️  WARNING: Error rate is high (>5%). Check tracker logs.")
		} else if successRate < 99 {
			fmt.Println("⚠️  NOTE: Error rate is elevated (1-5%). Monitor if consistent.")
		}

		if len(b.Stats.Latencies) > 0 {
			b.Stats.LatenciesMu.Lock()
			p95 := percentile(b.Stats.Latencies, 95)
			b.Stats.LatenciesMu.Unlock()

			if p95 > 50*time.Millisecond {
				fmt.Println("⚠️  WARNING: P95 latency is high (>50ms). Consider reducing concurrency.")
			} else if p95 > 10*time.Millisecond {
				fmt.Println("ℹ️  P95 latency is acceptable (10-50ms).")
			} else {
				fmt.Println("✓ P95 latency is excellent (<10ms).")
			}
		}

		rps := float64(b.Stats.TotalRequests) / elapsed.Seconds()
		if rps > 50000 {
			fmt.Println("✓ Excellent throughput (>50K RPS).")
		} else if rps > 10000 {
			fmt.Println("✓ Good throughput (10K-50K RPS).")
		} else {
			fmt.Println("ℹ️  Throughput could be improved (<10K RPS).")
		}
	}
	fmt.Println()
	fmt.Println("See benchmark/README.md for detailed result interpretation.")
}

func percentile(latencies []time.Duration, p float64) time.Duration {
	if len(latencies) == 0 {
		return 0
	}
	index := int(float64(len(latencies)) * p / 100.0)
	if index >= len(latencies) {
		index = len(latencies) - 1
	}
	return latencies[index]
}

func getMemoryMB() float64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return float64(m.Alloc) / 1024 / 1024
}

func generateInfoHash(workerID, hashID int) [20]byte {
	var hash [20]byte
	binary.BigEndian.PutUint32(hash[0:4], uint32(workerID))
	binary.BigEndian.PutUint32(hash[4:8], uint32(hashID))
	// Fill rest with deterministic pattern
	for i := 8; i < 20; i++ {
		hash[i] = byte(i)
	}
	return hash
}

func generatePeerID(workerID int) [20]byte {
	var id [20]byte
	copy(id[0:8], []byte("-UT1234-"))
	binary.BigEndian.PutUint32(id[8:12], uint32(workerID))
	binary.BigEndian.PutUint32(id[12:16], uint32(time.Now().UnixNano()))
	return id
}

func main() {
	var config Config

	flag.StringVar(&config.Target, "target", "localhost:1337", "Tracker address (host:port)")
	duration := flag.Duration("duration", 30*time.Second, "Benchmark duration")
	flag.IntVar(&config.Concurrency, "concurrency", 100, "Number of concurrent workers")
	flag.IntVar(&config.RateLimit, "rate", 0, "Rate limit per worker (req/s, 0=unlimited)")
	flag.IntVar(&config.NumHashes, "hashes", 5, "Number of info hashes per worker")
	flag.IntVar(&config.NumWant, "numwant", 50, "Number of peers to request")
	flag.Parse()

	config.Duration = *duration

	if config.Concurrency < 1 {
		log.Fatal("Concurrency must be at least 1")
	}

	benchmark := NewBenchmark(config)
	benchmark.Run()
}
