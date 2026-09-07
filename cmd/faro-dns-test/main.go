// Command faro-dns-test sends a broad, read-only DNS probe matrix to a Faro
// listener. It deliberately uses the standard library so it can run on the
// host without requiring dig or another DNS client to be installed.
package main

import (
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	transportUDP transport = "udp"
	transportTCP transport = "tcp"

	classIN = 1
	classCH = 3

	typeA     = 1
	typeAAAA  = 28
	typeNS    = 2
	typeCNAME = 5
	typeSOA   = 6
	typePTR   = 12
	typeMX    = 15
	typeTXT   = 16
	typeSRV   = 33
	typeNAPTR = 35
	typeTLSA  = 52
	typeSVCB  = 64
	typeHTTPS = 65
	typeCAA   = 257
	typeANY   = 255
	typeOPT   = 41

	flagQR = 1 << 15
	flagTC = 1 << 9
	flagRD = 1 << 8

	maxDNSMessageSize = 65535
	defaultEDNSSize   = 1232
)

var nextQueryID uint32 = uint32(time.Now().UnixNano())

type transport string

type config struct {
	host             string
	port             int
	transports       string
	domain           string
	timeout          time.Duration
	repeat           int
	parallel         int
	edns             bool
	includeEdge      bool
	includeMalformed bool
	customQueries    []string
}

type querySpec struct {
	label            string
	name             string
	qtype            uint16
	qclass           uint16
	recursionDesired bool
	edns             bool
	ednsPayload      uint16
	dnssec           bool
	raw              []byte
	rawSet           bool
	allowNoResponse  bool
}

type dnsResponse struct {
	rcode      int
	flags      uint16
	answers    int
	authority  int
	additional int
}

type probeResult struct {
	transport transport
	spec      querySpec
	elapsed   time.Duration
	response  *dnsResponse
	err       error
}

type stringList []string

func (values *stringList) String() string {
	return strings.Join(*values, ", ")
}

func (values *stringList) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("value cannot be empty")
	}
	*values = append(*values, value)
	return nil
}

func main() {
	var customQueries stringList
	host := flag.String("host", envString("FARO_DNS_TEST_HOST", "127.0.0.1"), "DNS server host")
	port := flag.Int("port", envInt("FARO_DNS_TEST_PORT", 5354), "DNS server port")
	transportList := flag.String("transport", envString("FARO_DNS_TEST_TRANSPORT", "both"), "transport: udp, tcp, or both")
	domain := flag.String("domain", envString("FARO_DNS_TEST_DOMAIN", "example.com"), "domain used by the default query matrix")
	timeout := flag.Duration("timeout", 2*time.Second, "timeout for each DNS exchange")
	repeat := flag.Int("repeat", 1, "number of times to send the complete query matrix")
	parallel := flag.Int("parallel", 1, "number of exchanges to run concurrently")
	edns := flag.Bool("edns", true, "include EDNS0 on generated queries")
	edge := flag.Bool("edge", true, "include unusual but valid queries")
	malformed := flag.Bool("malformed", false, "also send malformed packets; no response is acceptable for these probes")
	flag.Var(&customQueries, "query", "add a query in NAME:TYPE form; may be repeated")
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "Send a read-only DNS probe matrix to a local Faro instance.")
		fmt.Fprintln(flag.CommandLine.Output(), "")
		fmt.Fprintln(flag.CommandLine.Output(), "Examples:")
		fmt.Fprintln(flag.CommandLine.Output(), "  go run ./cmd/faro-dns-test")
		fmt.Fprintln(flag.CommandLine.Output(), "  go run ./cmd/faro-dns-test -port 53 -malformed")
		fmt.Fprintln(flag.CommandLine.Output(), "  go run ./cmd/faro-dns-test -query router.home:A -query ads.example:A")
		fmt.Fprintln(flag.CommandLine.Output(), "")
		flag.PrintDefaults()
	}
	flag.Parse()

	testConfig := config{
		host:             strings.TrimSpace(*host),
		port:             *port,
		transports:       *transportList,
		domain:           *domain,
		timeout:          *timeout,
		repeat:           *repeat,
		parallel:         *parallel,
		edns:             *edns,
		includeEdge:      *edge,
		includeMalformed: *malformed,
		customQueries:    customQueries,
	}
	if err := run(testConfig, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "DNS test failed: %v\n", err)
		os.Exit(1)
	}
}

func run(testConfig config, output io.Writer) error {
	if testConfig.host == "" {
		return errors.New("host is required")
	}
	if testConfig.port < 1 || testConfig.port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	if testConfig.timeout <= 0 {
		return errors.New("timeout must be positive")
	}
	if testConfig.repeat < 1 {
		return errors.New("repeat must be at least 1")
	}
	if testConfig.parallel < 1 {
		return errors.New("parallel must be at least 1")
	}

	transports, err := parseTransports(testConfig.transports)
	if err != nil {
		return err
	}
	specs, err := buildQuerySpecs(testConfig.domain, testConfig.customQueries, testConfig.includeEdge, testConfig.includeMalformed)
	if err != nil {
		return err
	}
	address := net.JoinHostPort(testConfig.host, strconv.Itoa(testConfig.port))

	type workItem struct {
		index     int
		transport transport
		spec      querySpec
	}
	work := make([]workItem, 0, len(specs)*len(transports)*testConfig.repeat)
	for round := 1; round <= testConfig.repeat; round++ {
		for _, selectedTransport := range transports {
			for _, spec := range specs {
				work = append(work, workItem{index: len(work), transport: selectedTransport, spec: spec})
			}
		}
	}

	results := make([]probeResult, len(work))
	if testConfig.parallel == 1 {
		for _, item := range work {
			results[item.index] = runProbe(address, item.transport, item.spec, testConfig.timeout, testConfig.edns)
		}
	} else {
		jobs := make(chan workItem)
		workerCount := testConfig.parallel
		if workerCount > len(work) {
			workerCount = len(work)
		}
		var workers sync.WaitGroup
		workers.Add(workerCount)
		for worker := 0; worker < workerCount; worker++ {
			go func() {
				defer workers.Done()
				for item := range jobs {
					results[item.index] = runProbe(address, item.transport, item.spec, testConfig.timeout, testConfig.edns)
				}
			}()
		}
		for _, item := range work {
			jobs <- item
		}
		close(jobs)
		workers.Wait()
	}

	probeCount := len(work)
	fmt.Fprintf(output, "Faro DNS tester\nTarget: %s\nTransports: %s\nQueries: %d per transport × %d transport%s × %d round%s = %d probes\n\n", address, strings.Join(transportNames(transports), ", "), len(specs), len(transports), pluralSuffix(len(transports)), testConfig.repeat, pluralSuffix(testConfig.repeat), probeCount)
	failures := 0
	for _, result := range results {
		if !result.passed() {
			failures++
		}
		printResult(output, result)
	}
	fmt.Fprintf(output, "\nSummary: %d passed, %d failed\n", len(results)-failures, failures)
	if failures > 0 {
		return fmt.Errorf("%d of %d probes failed", failures, len(results))
	}
	return nil
}

func (result probeResult) passed() bool {
	if result.err == nil {
		return true
	}
	return result.spec.allowNoResponse && expectedNoResponse(result.err)
}

func printResult(output io.Writer, result probeResult) {
	status := "PASS"
	if !result.passed() {
		status = "FAIL"
	}
	name := compactName(result.spec.name)
	if name == "" {
		name = "(raw packet)"
	}
	detail := ""
	if result.response != nil {
		response := result.response
		detail = fmt.Sprintf("%s answers=%d authority=%d additional=%d", rcodeName(response.rcode), response.answers, response.authority, response.additional)
		if response.flags&flagTC != 0 {
			detail += " truncated"
		}
	} else if result.err != nil {
		detail = result.err.Error()
		if result.spec.allowNoResponse && expectedNoResponse(result.err) {
			detail += " (allowed for edge probe)"
		}
	}
	fmt.Fprintf(output, "%-4s %-3s %-18s %-45s %7s  %s\n", status, strings.ToUpper(string(result.transport)), compactName(result.spec.label), name, result.elapsed.Round(time.Millisecond), detail)
}

func runProbe(address string, selectedTransport transport, spec querySpec, timeout time.Duration, includeEDNS bool) probeResult {
	started := time.Now()
	result := probeResult{transport: selectedTransport, spec: spec}
	packet, id, err := buildQuery(spec, includeEDNS)
	if err != nil {
		result.err = err
		result.elapsed = time.Since(started)
		return result
	}
	responsePacket, err := exchange(address, selectedTransport, packet, timeout)
	if err != nil {
		result.err = err
		result.elapsed = time.Since(started)
		return result
	}
	response, err := parseResponse(responsePacket, id)
	if err != nil {
		result.err = err
		result.elapsed = time.Since(started)
		return result
	}
	result.response = &response
	result.elapsed = time.Since(started)
	return result
}

func exchange(address string, selectedTransport transport, packet []byte, timeout time.Duration) ([]byte, error) {
	connection, err := net.DialTimeout(string(selectedTransport), address, timeout)
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(timeout)); err != nil {
		return nil, err
	}

	if selectedTransport == transportUDP {
		if _, err := connection.Write(packet); err != nil {
			return nil, err
		}
		response := make([]byte, maxDNSMessageSize)
		length, err := connection.Read(response)
		if err != nil {
			return nil, err
		}
		return response[:length], nil
	}

	if len(packet) > maxDNSMessageSize {
		return nil, errors.New("DNS query exceeds the TCP message limit")
	}
	var lengthPrefix [2]byte
	binary.BigEndian.PutUint16(lengthPrefix[:], uint16(len(packet)))
	if err := writeAll(connection, lengthPrefix[:]); err != nil {
		return nil, err
	}
	if err := writeAll(connection, packet); err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(connection, lengthPrefix[:]); err != nil {
		return nil, err
	}
	responseLength := int(binary.BigEndian.Uint16(lengthPrefix[:]))
	if responseLength == 0 || responseLength > maxDNSMessageSize {
		return nil, fmt.Errorf("invalid TCP DNS response length %d", responseLength)
	}
	response := make([]byte, responseLength)
	if _, err := io.ReadFull(connection, response); err != nil {
		return nil, err
	}
	return response, nil
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}

func buildQuery(spec querySpec, includeEDNS bool) ([]byte, uint16, error) {
	if spec.rawSet {
		packet := append([]byte(nil), spec.raw...)
		var id uint16
		if len(packet) >= 2 {
			id = binary.BigEndian.Uint16(packet[:2])
		}
		return packet, id, nil
	}
	if spec.qclass == 0 {
		spec.qclass = classIN
	}
	name, err := encodeName(spec.name)
	if err != nil {
		return nil, 0, err
	}
	id := uint16(atomic.AddUint32(&nextQueryID, 1))
	useEDNS := includeEDNS && spec.edns
	additional := uint16(0)
	if useEDNS {
		additional = 1
	}
	flags := uint16(0)
	if spec.recursionDesired {
		flags |= flagRD
	}
	packet := make([]byte, 0, 64+len(name))
	packet = appendUint16(packet, id)
	packet = appendUint16(packet, flags)
	packet = appendUint16(packet, 1)
	packet = appendUint16(packet, 0)
	packet = appendUint16(packet, 0)
	packet = appendUint16(packet, additional)
	packet = append(packet, name...)
	packet = appendUint16(packet, spec.qtype)
	packet = appendUint16(packet, spec.qclass)
	if useEDNS {
		payload := spec.ednsPayload
		if payload == 0 {
			payload = defaultEDNSSize
		}
		packet = append(packet, 0)
		packet = appendUint16(packet, typeOPT)
		packet = appendUint16(packet, payload)
		if spec.dnssec {
			packet = appendUint32(packet, 0x00008000)
		} else {
			packet = appendUint32(packet, 0)
		}
		packet = appendUint16(packet, 0)
	}
	return packet, id, nil
}

func parseResponse(packet []byte, expectedID uint16) (dnsResponse, error) {
	if len(packet) < 12 {
		return dnsResponse{}, errors.New("DNS response is shorter than its header")
	}
	actualID := binary.BigEndian.Uint16(packet[:2])
	if expectedID != 0 && actualID != expectedID {
		return dnsResponse{}, fmt.Errorf("DNS response ID %04x does not match query ID %04x", actualID, expectedID)
	}
	flags := binary.BigEndian.Uint16(packet[2:4])
	if flags&flagQR == 0 {
		return dnsResponse{}, errors.New("DNS response did not set the response bit")
	}
	return dnsResponse{
		rcode:      int(flags & 0x000f),
		flags:      flags,
		answers:    int(binary.BigEndian.Uint16(packet[6:8])),
		authority:  int(binary.BigEndian.Uint16(packet[8:10])),
		additional: int(binary.BigEndian.Uint16(packet[10:12])),
	}, nil
}

func buildQuerySpecs(domain string, custom []string, includeEdge, includeMalformed bool) ([]querySpec, error) {
	canonicalDomain := canonicalName(domain)
	if _, err := encodeName(canonicalDomain); err != nil {
		return nil, fmt.Errorf("invalid domain %q: %w", domain, err)
	}
	base := strings.TrimSuffix(canonicalDomain, ".")
	specs := defaultQueries(canonicalDomain, base)
	for _, value := range custom {
		spec, err := parseCustomQuery(value)
		if err != nil {
			return nil, err
		}
		specs = append(specs, spec)
	}
	if includeEdge {
		specs = append(specs, edgeQueries(canonicalDomain, base)...)
	}
	if includeMalformed {
		specs = append(specs, malformedQueries()...)
	}
	return specs, nil
}

func defaultQueries(domain, base string) []querySpec {
	return []querySpec{
		newQuery("A", domain, typeA),
		newQuery("AAAA", domain, typeAAAA),
		newQuery("CNAME", joinName("www", base), typeCNAME),
		newQuery("MX", domain, typeMX),
		newQuery("NS", domain, typeNS),
		newQuery("SOA", domain, typeSOA),
		newQuery("TXT", domain, typeTXT),
		newQuery("SRV", joinName("_sip._tcp", base), typeSRV),
		newQuery("PTR", "8.8.8.8.in-addr.arpa.", typePTR),
		newQuery("NAPTR", domain, typeNAPTR),
		newQuery("TLSA", joinName("_443._tcp", base), typeTLSA),
		newQuery("SVCB", joinName("_svc", base), typeSVCB),
		newQuery("HTTPS", domain, typeHTTPS),
		newQuery("CAA", domain, typeCAA),
		newQuery("ANY", domain, typeANY),
		newQuery("root NS", ".", typeNS),
		newQuery("localhost A", "localhost.", typeA),
		newQuery("NXDOMAIN", fmt.Sprintf("faro-dns-test-%d.invalid.", time.Now().UnixNano()), typeA),
	}
}

func edgeQueries(domain, base string) []querySpec {
	noRecursion := newQuery("no recursion", domain, typeA)
	noRecursion.recursionDesired = false
	dnssec := newQuery("DNSSEC DO", domain, typeA)
	dnssec.dnssec = true
	largeEDNS := newQuery("large EDNS", domain, typeTXT)
	largeEDNS.ednsPayload = 4096
	mixedCase := newQuery("mixed case", mixedCaseName(domain), typeA)
	chTXT := newQuery("CH TXT", "version.bind.", typeTXT)
	chTXT.qclass = classCH
	unknownType := newQuery("TYPE65534", domain, 65534)
	return []querySpec{
		noRecursion,
		dnssec,
		largeEDNS,
		mixedCase,
		chTXT,
		unknownType,
		newQuery("root A", ".", typeA),
		newQuery("SVCB service", joinName("_foo", base), typeSVCB),
	}
}

func malformedQueries() []querySpec {
	shortHeader := querySpec{label: "short header", raw: []byte{0x5a, 0x01, 0x01}, rawSet: true, allowNoResponse: true}
	zeroQuestions := querySpec{label: "zero questions", raw: rawHeader(0x5a02, flagRD, 0, 0, 0, 0), rawSet: true, allowNoResponse: true}
	truncatedQuestion := rawHeader(0x5a03, flagRD, 1, 0, 0, 0)
	truncatedQuestion = append(truncatedQuestion, 3, 'w', 'w', 'w')
	truncatedQuestionSpec := querySpec{label: "truncated question", raw: truncatedQuestion, rawSet: true, allowNoResponse: true}
	badOpcode := querySpec{label: "unsupported opcode", raw: rawHeader(0x5a04, 0x0800|flagRD, 0, 0, 0, 0), rawSet: true, allowNoResponse: true}
	return []querySpec{shortHeader, zeroQuestions, truncatedQuestionSpec, badOpcode}
}

func parseCustomQuery(value string) (querySpec, error) {
	separator := strings.LastIndex(value, ":")
	if separator <= 0 || separator == len(value)-1 {
		return querySpec{}, fmt.Errorf("invalid query %q; use NAME:TYPE", value)
	}
	name := strings.TrimSpace(value[:separator])
	typeValue := strings.TrimSpace(value[separator+1:])
	qtype, err := parseType(typeValue)
	if err != nil {
		return querySpec{}, fmt.Errorf("invalid query %q: %w", value, err)
	}
	spec := newQuery("custom "+typeName(qtype), name, qtype)
	if _, err := encodeName(spec.name); err != nil {
		return querySpec{}, fmt.Errorf("invalid query %q: %w", value, err)
	}
	return spec, nil
}

func parseType(value string) (uint16, error) {
	upper := strings.ToUpper(strings.TrimSpace(value))
	types := map[string]uint16{
		"A": typeA, "NS": typeNS, "CNAME": typeCNAME, "SOA": typeSOA, "PTR": typePTR,
		"MX": typeMX, "TXT": typeTXT, "SRV": typeSRV, "NAPTR": typeNAPTR, "TLSA": typeTLSA,
		"SVCB": typeSVCB, "HTTPS": typeHTTPS, "CAA": typeCAA, "ANY": typeANY,
	}
	if qtype, ok := types[upper]; ok {
		return qtype, nil
	}
	if strings.HasPrefix(upper, "TYPE") {
		upper = strings.TrimPrefix(upper, "TYPE")
	}
	parsed, err := strconv.ParseUint(upper, 10, 16)
	if err != nil {
		return 0, fmt.Errorf("unknown record type %q", value)
	}
	return uint16(parsed), nil
}

func parseTransports(value string) ([]transport, error) {
	var transports []transport
	seen := map[transport]bool{}
	for _, part := range strings.Split(strings.ToLower(strings.TrimSpace(value)), ",") {
		part = strings.TrimSpace(part)
		if part == "both" {
			part = "udp,tcp"
			for _, nested := range strings.Split(part, ",") {
				selected := transport(nested)
				if !seen[selected] {
					seen[selected] = true
					transports = append(transports, selected)
				}
			}
			continue
		}
		selected := transport(part)
		if selected != transportUDP && selected != transportTCP {
			return nil, fmt.Errorf("invalid transport %q; use udp, tcp, or both", part)
		}
		if !seen[selected] {
			seen[selected] = true
			transports = append(transports, selected)
		}
	}
	if len(transports) == 0 {
		return nil, errors.New("at least one transport is required")
	}
	return transports, nil
}

func encodeName(name string) ([]byte, error) {
	canonical := canonicalName(name)
	if canonical == "." {
		return []byte{0}, nil
	}
	labels := strings.Split(strings.TrimSuffix(canonical, "."), ".")
	encoded := make([]byte, 0, len(canonical)+1)
	for _, label := range labels {
		if label == "" {
			return nil, errors.New("DNS name contains an empty label")
		}
		if len(label) > 63 {
			return nil, fmt.Errorf("DNS label %q is longer than 63 bytes", label)
		}
		encoded = append(encoded, byte(len(label)))
		encoded = append(encoded, label...)
	}
	if len(encoded)+1 > 255 {
		return nil, errors.New("DNS name is longer than 255 bytes")
	}
	return append(encoded, 0), nil
}

func rawHeader(id, flags, questions, answers, authority, additional uint16) []byte {
	header := make([]byte, 12)
	binary.BigEndian.PutUint16(header[0:2], id)
	binary.BigEndian.PutUint16(header[2:4], flags)
	binary.BigEndian.PutUint16(header[4:6], questions)
	binary.BigEndian.PutUint16(header[6:8], answers)
	binary.BigEndian.PutUint16(header[8:10], authority)
	binary.BigEndian.PutUint16(header[10:12], additional)
	return header
}

func newQuery(label, name string, qtype uint16) querySpec {
	return querySpec{
		label:            label,
		name:             canonicalName(name),
		qtype:            qtype,
		qclass:           classIN,
		recursionDesired: true,
		edns:             true,
		ednsPayload:      defaultEDNSSize,
	}
}

func canonicalName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || trimmed == "." {
		return "."
	}
	return strings.TrimSuffix(trimmed, ".") + "."
}

func joinName(prefix, base string) string {
	if base == "" {
		return canonicalName(prefix)
	}
	return canonicalName(prefix + "." + base)
}

func mixedCaseName(name string) string {
	var builder strings.Builder
	upper := false
	for _, character := range name {
		if character >= 'a' && character <= 'z' {
			if upper {
				character -= 'a' - 'A'
			}
			upper = !upper
		}
		builder.WriteRune(character)
	}
	return builder.String()
}

func appendUint16(data []byte, value uint16) []byte {
	var encoded [2]byte
	binary.BigEndian.PutUint16(encoded[:], value)
	return append(data, encoded[:]...)
}

func appendUint32(data []byte, value uint32) []byte {
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], value)
	return append(data, encoded[:]...)
}

func expectedNoResponse(err error) bool {
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return true
	}
	return errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || strings.Contains(strings.ToLower(err.Error()), "connection reset")
}

func rcodeName(rcode int) string {
	if name, ok := map[int]string{0: "NOERROR", 1: "FORMERR", 2: "SERVFAIL", 3: "NXDOMAIN", 4: "NOTIMP", 5: "REFUSED", 6: "YXDOMAIN", 7: "YXRRSET", 8: "NXRRSET", 9: "NOTAUTH", 10: "NOTZONE"}[rcode]; ok {
		return name
	}
	return fmt.Sprintf("RCODE%d", rcode)
}

func typeName(qtype uint16) string {
	if name, ok := map[uint16]string{typeA: "A", typeNS: "NS", typeCNAME: "CNAME", typeSOA: "SOA", typePTR: "PTR", typeMX: "MX", typeTXT: "TXT", typeSRV: "SRV", typeNAPTR: "NAPTR", typeTLSA: "TLSA", typeSVCB: "SVCB", typeHTTPS: "HTTPS", typeCAA: "CAA", typeANY: "ANY"}[qtype]; ok {
		return name
	}
	return fmt.Sprintf("TYPE%d", qtype)
}

func transportNames(transports []transport) []string {
	names := make([]string, len(transports))
	for index, selected := range transports {
		names[index] = strings.ToUpper(string(selected))
	}
	return names
}

func compactName(value string) string {
	const maxLength = 45
	if len(value) <= maxLength {
		return value
	}
	return value[:maxLength-1] + "…"
}

func pluralSuffix(value int) string {
	if value == 1 {
		return ""
	}
	return "s"
}

func envString(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key)))
	if err != nil {
		return fallback
	}
	return value
}
