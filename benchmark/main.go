// BitTorrent UDP Tracker Benchmark Tool
// Simulates concurrent UDP clients to test tracker performance
//
// Usage: go run benchmark/main.go -target localhost:1337 -duration 30s -concurrency 100

package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// BEP 15 UDP Tracker Protocol constants
const (
	protocolID      = 0x41727101980 // magic constant identifying BitTorrent UDP
	actionConnect   = 0
	actionAnnounce  = 1
	actionScrape    = 2
	responseTimeout = 5 * time.Second
)

// LatencyStats stores latencies for a specific operation type (connect/announce/scrape)
type LatencyStats struct {
	Latencies []time.Duration
	Mu        sync.Mutex
}

func (l *LatencyStats) Record(d time.Duration) {
	l.Mu.Lock()
	l.Latencies = append(l.Latencies, d)
	l.Mu.Unlock()
}

func (l *LatencyStats) getSorted() []time.Duration {
	l.Mu.Lock()
	defer l.Mu.Unlock()
	if len(l.Latencies) == 0 {
		return nil
	}
	sorted := make([]time.Duration, len(l.Latencies))
	copy(sorted, l.Latencies)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted
}

func (l *LatencyStats) Percentile(p float64) time.Duration {
	sorted := l.getSorted()
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)) * p / 100.0)
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func (l *LatencyStats) Avg() time.Duration {
	l.Mu.Lock()
	defer l.Mu.Unlock()
	if len(l.Latencies) == 0 {
		return 0
	}
	var sum time.Duration
	for _, d := range l.Latencies {
		sum += d
	}
	return sum / time.Duration(len(l.Latencies))
}

func (l *LatencyStats) Min() time.Duration {
	sorted := l.getSorted()
	if len(sorted) == 0 {
		return 0
	}
	return sorted[0]
}

func (l *LatencyStats) Max() time.Duration {
	sorted := l.getSorted()
	if len(sorted) == 0 {
		return 0
	}
	return sorted[len(sorted)-1]
}

func (l *LatencyStats) Count() int {
	l.Mu.Lock()
	defer l.Mu.Unlock()
	return len(l.Latencies)
}

type Stats struct {
	ResponseSizes        []int
	TotalResponseSizes   []int
	StartTime            time.Time
	ConnectLatency       LatencyStats
	AnnounceLatency      LatencyStats
	ScrapeLatency        LatencyStats
	ResponseSizesMu      sync.Mutex
	TotalResponseSizesMu sync.Mutex
	TotalRequests        uint64
	SuccessfulReqs       uint64
	FailedReqs           uint64
	ConnectCount         uint64
	AnnounceCount        uint64
	ScrapeCount          uint64
}

type Config struct {
	Target      string
	Duration    time.Duration
	Concurrency int
	RateLimit   int
	NumHashes   int
	NumWant     int
}

type Benchmark struct {
	StopCh chan struct{}
	Config Config
	Stats  Stats
}

func NewBenchmark(cfg Config) *Benchmark {
	return &Benchmark{
		StopCh: make(chan struct{}),
		Config: cfg,
		Stats: Stats{
			ResponseSizes:      make([]int, 0, 100000),
			TotalResponseSizes: make([]int, 0, 100000),
		},
	}
}

func (b *Benchmark) Run() {
	b.Stats.StartTime = time.Now()

	fmt.Printf("Starting benchmark...\n")
	fmt.Printf("Target: %s\n", b.Config.Target)
	fmt.Printf("Duration: %s\n", b.Config.Duration)
	fmt.Printf("Concurrency: %d\n", b.Config.Concurrency)
	fmt.Printf("Rate limit: %d req/s per worker\n", b.Config.RateLimit)
	fmt.Printf("Info hashes: %d\n", b.Config.NumHashes)
	fmt.Println()

	go b.reportProgress()

	var wg sync.WaitGroup
	for i := 0; i < b.Config.Concurrency; i++ {
		wg.Add(1)
		go b.worker(i, &wg)
	}

	time.Sleep(b.Config.Duration)
	close(b.StopCh)
	wg.Wait()
	b.printResults()
}

func (b *Benchmark) worker(id int, wg *sync.WaitGroup) {
	defer wg.Done()

	conn, err := net.Dial("udp", b.Config.Target)
	if err != nil {
		log.Printf("Worker %d: failed to connect: %v", id, err)
		return
	}
	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			log.Printf("Worker %d: failed to close connection: %v", id, closeErr)
		}
	}()

	udpConn, ok := conn.(*net.UDPConn)
	if !ok {
		log.Printf("Worker %d: connection is not UDP", id)
		return
	}
	if err = udpConn.SetDeadline(time.Now().Add(b.Config.Duration + 10*time.Second)); err != nil {
		log.Printf("Worker %d: failed to set deadline: %v", id, err)
		return
	}

	var rateLimiter *time.Ticker
	if b.Config.RateLimit > 0 {
		rateLimiter = time.NewTicker(time.Second / time.Duration(b.Config.RateLimit))
		defer rateLimiter.Stop()
	}

	hashes := make([][20]byte, b.Config.NumHashes)
	peerID := generatePeerID(id)
	for i := range hashes {
		hashes[i] = generateInfoHash(id, i)
	}

	connID, err := b.doConnect(udpConn)
	if err != nil {
		log.Printf("Worker %d: initial connect failed: %v", id, err)
		return
	}
	atomic.AddUint64(&b.Stats.ConnectCount, 1)
	atomic.AddUint64(&b.Stats.SuccessfulReqs, 1)
	atomic.AddUint64(&b.Stats.TotalRequests, 1)

	var connIDAtomic atomic.Uint64
	connIDAtomic.Store(connID)

	// Refresh connection ID before it expires (2 min per BEP 15)
	reconnectTimer := time.AfterFunc(2*time.Minute, func() {
		newConnID, err := b.doConnect(udpConn)
		if err != nil {
			log.Printf("Worker %d: re-connect failed: %v", id, err)
		} else {
			connIDAtomic.Store(newConnID)
			atomic.AddUint64(&b.Stats.ConnectCount, 1)
			atomic.AddUint64(&b.Stats.SuccessfulReqs, 1)
			atomic.AddUint64(&b.Stats.TotalRequests, 1)
		}
	})
	defer reconnectTimer.Stop()

	for {
		select {
		case <-b.StopCh:
			return
		default:
		}

		if rateLimiter != nil {
			<-rateLimiter.C
		}

		b.performCycleWithConnID(udpConn, connIDAtomic.Load(), peerID, hashes)
	}
}

// performCycleWithConnID sends announces for all hashes, then one scrape
func (b *Benchmark) performCycleWithConnID(conn *net.UDPConn, connectionID uint64, peerID [20]byte, hashes [][20]byte) {
	for _, hash := range hashes {
		select {
		case <-b.StopCh:
			return
		default:
		}

		err := b.doAnnounce(conn, connectionID, hash, peerID)
		if err != nil {
			atomic.AddUint64(&b.Stats.FailedReqs, 1)
		} else {
			atomic.AddUint64(&b.Stats.AnnounceCount, 1)
			atomic.AddUint64(&b.Stats.SuccessfulReqs, 1)
			atomic.AddUint64(&b.Stats.TotalRequests, 1)
		}
	}

	err := b.doScrape(conn, connectionID, hashes[0])
	if err != nil {
		atomic.AddUint64(&b.Stats.FailedReqs, 1)
	} else {
		atomic.AddUint64(&b.Stats.ScrapeCount, 1)
		atomic.AddUint64(&b.Stats.SuccessfulReqs, 1)
		atomic.AddUint64(&b.Stats.TotalRequests, 1)
	}
}

// doConnect sends a BEP 15 connect request and returns the connection ID.
// Connection ID is valid for 2 minutes.
func (b *Benchmark) doConnect(conn *net.UDPConn) (uint64, error) {
	start := time.Now()
	transactionID := uint32(time.Now().UnixNano())

	request := make([]byte, 16)
	binary.BigEndian.PutUint64(request[0:8], protocolID)
	binary.BigEndian.PutUint32(request[8:12], actionConnect)
	binary.BigEndian.PutUint32(request[12:16], transactionID)

	if _, err := conn.Write(request); err != nil {
		b.Stats.ConnectLatency.Record(time.Since(start))
		b.recordResponseSize(0, false)
		return 0, err
	}

	response := make([]byte, 16)
	n, err := readResponse(conn, response, actionConnect, transactionID)
	if err == nil {
		b.Stats.ConnectLatency.Record(time.Since(start))
		b.recordResponseSize(n, true)
		return binary.BigEndian.Uint64(response[8:16]), nil
	}

	b.Stats.ConnectLatency.Record(time.Since(start))
	b.recordResponseSize(0, false)
	return 0, err
}

// readResponse reads a UDP response and validates it matches the expected action and transaction ID.
func readResponse(conn *net.UDPConn, buf []byte, action, transactionID uint32) (int, error) {
	if err := conn.SetReadDeadline(time.Now().Add(responseTimeout)); err != nil {
		return 0, err
	}
	n, err := conn.Read(buf)
	if err != nil {
		return 0, err
	}
	if n < 16 {
		return 0, fmt.Errorf("response too short: got %d bytes, expected at least 16", n)
	}
	respAction := binary.BigEndian.Uint32(buf[0:4])
	respTxID := binary.BigEndian.Uint32(buf[4:8])
	if respAction != action || respTxID != transactionID {
		return 0, fmt.Errorf("invalid response")
	}
	return n, nil
}

// doAnnounce registers a peer with the tracker for a given info hash.
func (b *Benchmark) doAnnounce(conn *net.UDPConn, connectionID uint64, infoHash, peerID [20]byte) error {
	start := time.Now()
	transactionID := uint32(time.Now().UnixNano())

	request := make([]byte, 98)
	binary.BigEndian.PutUint64(request[0:8], connectionID)
	binary.BigEndian.PutUint32(request[8:12], actionAnnounce)
	binary.BigEndian.PutUint32(request[12:16], transactionID)
	copy(request[16:36], infoHash[:])
	copy(request[36:56], peerID[:])
	binary.BigEndian.PutUint64(request[56:64], 0)
	binary.BigEndian.PutUint64(request[64:72], 100) // left=100 means leecher
	binary.BigEndian.PutUint64(request[72:80], 0)
	binary.BigEndian.PutUint32(request[80:84], 0) // event: none
	binary.BigEndian.PutUint32(request[84:88], 0) // IP
	binary.BigEndian.PutUint32(request[88:92], 0) // key
	binary.BigEndian.PutUint32(request[92:96], uint32(b.Config.NumWant))
	binary.BigEndian.PutUint16(request[96:98], 6881)

	var n int
	var err error
	if _, err = conn.Write(request); err == nil {
		response := make([]byte, 1500)
		n, err = readResponse(conn, response, actionAnnounce, transactionID)
		if err == nil {
			b.Stats.AnnounceLatency.Record(time.Since(start))
			b.recordResponseSize(n, true)
			return nil
		}
	}

	b.Stats.AnnounceLatency.Record(time.Since(start))
	b.recordResponseSize(0, false)
	return err
}

// doScrape requests torrent statistics from the tracker.
func (b *Benchmark) doScrape(conn *net.UDPConn, connectionID uint64, infoHash [20]byte) error {
	start := time.Now()
	transactionID := uint32(time.Now().UnixNano())

	request := make([]byte, 36)
	binary.BigEndian.PutUint64(request[0:8], connectionID)
	binary.BigEndian.PutUint32(request[8:12], actionScrape)
	binary.BigEndian.PutUint32(request[12:16], transactionID)
	copy(request[16:36], infoHash[:])

	var n int
	var err error
	if _, err = conn.Write(request); err == nil {
		response := make([]byte, 20)
		n, err = readResponse(conn, response, actionScrape, transactionID)
		if err == nil {
			b.Stats.ScrapeLatency.Record(time.Since(start))
			b.recordResponseSize(n, true)
			return nil
		}
	}

	b.Stats.ScrapeLatency.Record(time.Since(start))
	b.recordResponseSize(0, false)
	return err
}

// recordResponseSize tracks response sizes for successful requests separately from all requests.
// This allows accurate min/max/avg for successful responses while still counting failures.
func (b *Benchmark) recordResponseSize(n int, success bool) {
	b.Stats.ResponseSizesMu.Lock()
	if success && n > 0 {
		b.Stats.ResponseSizes = append(b.Stats.ResponseSizes, n)
	}
	b.Stats.ResponseSizesMu.Unlock()

	b.Stats.TotalResponseSizesMu.Lock()
	b.Stats.TotalResponseSizes = append(b.Stats.TotalResponseSizes, n)
	b.Stats.TotalResponseSizesMu.Unlock()
}

// getResponseSizes returns copies of response size slices to avoid data races.
func (b *Benchmark) getResponseSizes() (sizes, totalSizes []int) {
	b.Stats.ResponseSizesMu.Lock()
	sizes = make([]int, len(b.Stats.ResponseSizes))
	copy(sizes, b.Stats.ResponseSizes)
	b.Stats.ResponseSizesMu.Unlock()

	b.Stats.TotalResponseSizesMu.Lock()
	totalSizes = make([]int, len(b.Stats.TotalResponseSizes))
	copy(totalSizes, b.Stats.TotalResponseSizes)
	b.Stats.TotalResponseSizesMu.Unlock()
	return
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

func (b *Benchmark) printResults() {
	elapsed := time.Since(b.Stats.StartTime)

	totalRequests := atomic.LoadUint64(&b.Stats.TotalRequests)
	successfulReqs := atomic.LoadUint64(&b.Stats.SuccessfulReqs)
	failedReqs := atomic.LoadUint64(&b.Stats.FailedReqs)
	connectCount := atomic.LoadUint64(&b.Stats.ConnectCount)
	announceCount := atomic.LoadUint64(&b.Stats.AnnounceCount)
	scrapeCount := atomic.LoadUint64(&b.Stats.ScrapeCount)

	b.printHeader(elapsed)
	b.printRequestStats(totalRequests, successfulReqs, failedReqs, elapsed)
	b.printRequestBreakdown(connectCount, announceCount, scrapeCount)
	b.printLatencyStats(connectCount, announceCount, scrapeCount)
	b.printResponseSizeStats()
	b.printRecommendations(totalRequests, successfulReqs, elapsed)

	fmt.Println()
	fmt.Println("See benchmark/README.md for detailed result interpretation.")
}

func (b *Benchmark) printHeader(elapsed time.Duration) {
	fmt.Println()
	fmt.Println("========================================")
	fmt.Println("       BENCHMARK RESULTS")
	fmt.Println("========================================")
	fmt.Printf("Duration: %s\n", elapsed.Round(time.Millisecond))
	fmt.Printf("Concurrency: %d workers\n", b.Config.Concurrency)
	fmt.Println()
}

func (b *Benchmark) printRequestStats(totalRequests, successfulReqs, failedReqs uint64, elapsed time.Duration) {
	fmt.Println("--- Request Statistics ---")
	fmt.Printf("Total Requests:     %d\n", totalRequests)

	successRate := float64(0)
	failRate := float64(0)
	if totalRequests > 0 {
		successRate = float64(successfulReqs) / float64(totalRequests) * 100
		failRate = float64(failedReqs) / float64(totalRequests) * 100
	}

	fmt.Printf("Successful:         %d (%.2f%%)\n", successfulReqs, successRate)
	fmt.Printf("Failed:             %d (%.2f%%)\n", failedReqs, failRate)
	fmt.Printf("Requests/Second:    %.2f\n", float64(totalRequests)/elapsed.Seconds())
	fmt.Println()
}

func (b *Benchmark) printRequestBreakdown(connectCount, announceCount, scrapeCount uint64) {
	fmt.Println("--- Request Breakdown ---")
	fmt.Printf("Connect:            %d\n", connectCount)
	fmt.Printf("Announce:           %d\n", announceCount)
	fmt.Printf("Scrape:             %d\n", scrapeCount)
	fmt.Println()
}

func (b *Benchmark) printLatencyStats(connectCount, announceCount, scrapeCount uint64) {
	fmt.Println("--- Latency Statistics ---")

	printLatencyBreakdown := func(name string, lat *LatencyStats, count uint64) {
		if count == 0 {
			return
		}
		fmt.Printf("\n%s Latency (n=%d):\n", name, count)
		fmt.Printf("  Min:  %s\n", lat.Min())
		fmt.Printf("  Avg:  %s\n", lat.Avg())
		fmt.Printf("  P50:  %s\n", lat.Percentile(50))
		fmt.Printf("  P95:  %s\n", lat.Percentile(95))
		fmt.Printf("  P99:  %s\n", lat.Percentile(99))
		fmt.Printf("  Max:  %s\n", lat.Max())
	}

	printLatencyBreakdown("Connect", &b.Stats.ConnectLatency, connectCount)
	printLatencyBreakdown("Announce", &b.Stats.AnnounceLatency, announceCount)
	printLatencyBreakdown("Scrape", &b.Stats.ScrapeLatency, scrapeCount)
	fmt.Println()
}

func (b *Benchmark) printResponseSizeStats() {
	respSizes, totalRespSizes := b.getResponseSizes()

	respSizeCount := len(respSizes)
	if respSizeCount > 0 {
		var totalSize int
		minSize := respSizes[0]
		maxSize := respSizes[0]
		for _, s := range respSizes {
			totalSize += s
			if s < minSize {
				minSize = s
			}
			if s > maxSize {
				maxSize = s
			}
		}
		avgSize := float64(totalSize) / float64(respSizeCount)
		fmt.Println("--- Response Size Statistics (successful) ---")
		fmt.Printf("Min:    %d bytes\n", minSize)
		fmt.Printf("Avg:    %.0f bytes\n", avgSize)
		fmt.Printf("Max:    %d bytes\n", maxSize)
		fmt.Printf("Count:  %d\n", respSizeCount)
	}

	totalRespSizeCount := len(totalRespSizes)
	if totalRespSizeCount > 0 {
		var totalSize int
		for _, s := range totalRespSizes {
			totalSize += s
		}
		avgSize := float64(totalSize) / float64(totalRespSizeCount)
		fmt.Println("--- Response Size Statistics (all) ---")
		fmt.Printf("Avg:    %.0f bytes\n", avgSize)
		fmt.Printf("Count:  %d (includes %d failures with 0 bytes)\n", totalRespSizeCount, totalRespSizeCount-respSizeCount)
	}
	fmt.Println()
}

func (b *Benchmark) printRecommendations(totalRequests, successfulReqs uint64, elapsed time.Duration) {
	if totalRequests == 0 {
		return
	}

	successRate := float64(successfulReqs) / float64(totalRequests) * 100
	switch {
	case successRate < 95:
		fmt.Println("⚠️  WARNING: Error rate is high (>5%). Check tracker logs.")
	case successRate < 99:
		fmt.Println("⚠️  NOTE: Error rate is elevated (1-5%). Monitor if consistent.")
	}

	if b.Stats.AnnounceLatency.Count() > 0 {
		p95 := b.Stats.AnnounceLatency.Percentile(95)
		switch {
		case p95 > 50*time.Millisecond:
			fmt.Println("⚠️  WARNING: P95 latency is high (>50ms). Consider reducing concurrency.")
		case p95 > 10*time.Millisecond:
			fmt.Println("ℹ️  P95 latency is acceptable (10-50ms).")
		default:
			fmt.Println("✓ P95 latency is excellent (<10ms).")
		}
	}

	rps := float64(totalRequests) / elapsed.Seconds()
	switch {
	case rps > 50000:
		fmt.Println("✓ Excellent throughput (>50K RPS).")
	case rps > 10000:
		fmt.Println("✓ Good throughput (10K-50K RPS).")
	default:
		fmt.Println("ℹ️  Throughput could be improved (<10K RPS).")
	}
}

// generateInfoHash creates a deterministic 20-byte info hash for testing.
func generateInfoHash(workerID, hashID int) [20]byte {
	var hash [20]byte
	binary.BigEndian.PutUint32(hash[0:4], uint32(workerID))
	binary.BigEndian.PutUint32(hash[4:8], uint32(hashID))
	for i := 8; i < 20; i++ {
		hash[i] = byte(i)
	}
	return hash
}

// generatePeerID creates a uTorrent-style peer ID for testing.
func generatePeerID(workerID int) [20]byte {
	var id [20]byte
	copy(id[0:8], "-UT1234-")
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
