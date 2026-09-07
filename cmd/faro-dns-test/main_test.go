package main

import (
	"encoding/binary"
	"reflect"
	"testing"
)

func TestEncodeName(t *testing.T) {
	got, err := encodeName("www.example.com.")
	if err != nil {
		t.Fatalf("encodeName returned error: %v", err)
	}
	want := []byte{3, 'w', 'w', 'w', 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', 'm', 0}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("encodeName = %v, want %v", got, want)
	}
	root, err := encodeName(".")
	if err != nil || !reflect.DeepEqual(root, []byte{0}) {
		t.Fatalf("encodeName root = %v, %v; want [0], nil", root, err)
	}
}

func TestEncodeNameRejectsOversizedLabel(t *testing.T) {
	if _, err := encodeName("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.example."); err == nil {
		t.Fatal("encodeName accepted a label longer than 63 bytes")
	}
}

func TestBuildQueryIncludesEDNSAndDNSSEC(t *testing.T) {
	spec := newQuery("A", "example.com.", typeA)
	spec.dnssec = true
	packet, id, err := buildQuery(spec, true)
	if err != nil {
		t.Fatalf("buildQuery returned error: %v", err)
	}
	if id != binary.BigEndian.Uint16(packet[:2]) {
		t.Fatalf("query ID %04x does not match packet", id)
	}
	if binary.BigEndian.Uint16(packet[4:6]) != 1 || binary.BigEndian.Uint16(packet[10:12]) != 1 {
		t.Fatalf("unexpected question/additional counts: %d/%d", binary.BigEndian.Uint16(packet[4:6]), binary.BigEndian.Uint16(packet[10:12]))
	}
	name, _ := encodeName(spec.name)
	optOffset := 12 + len(name) + 4
	if packet[optOffset] != 0 || binary.BigEndian.Uint16(packet[optOffset+1:optOffset+3]) != typeOPT {
		t.Fatalf("query did not end with an OPT record: %v", packet[optOffset:])
	}
	if binary.BigEndian.Uint32(packet[optOffset+5:optOffset+9]) != 0x00008000 {
		t.Fatalf("DNSSEC DO bit is missing from OPT record")
	}
}

func TestBuildQueryCanOmitEDNS(t *testing.T) {
	packet, _, err := buildQuery(newQuery("A", "example.com", typeA), false)
	if err != nil {
		t.Fatalf("buildQuery returned error: %v", err)
	}
	if binary.BigEndian.Uint16(packet[10:12]) != 0 {
		t.Fatalf("additional count = %d, want 0", binary.BigEndian.Uint16(packet[10:12]))
	}
}

func TestParseResponse(t *testing.T) {
	packet := rawHeader(0x1234, flagQR|flagTC, 1, 2, 3, 4)
	packet[3] |= 3
	response, err := parseResponse(packet, 0x1234)
	if err != nil {
		t.Fatalf("parseResponse returned error: %v", err)
	}
	if response.rcode != 3 || response.answers != 2 || response.authority != 3 || response.additional != 4 || response.flags&flagTC == 0 {
		t.Fatalf("unexpected response: %+v", response)
	}
	if _, err := parseResponse(packet, 0x4321); err == nil {
		t.Fatal("parseResponse accepted a mismatched query ID")
	}
}

func TestParseCustomQuery(t *testing.T) {
	spec, err := parseCustomQuery("router.home:TYPE65400")
	if err != nil {
		t.Fatalf("parseCustomQuery returned error: %v", err)
	}
	if spec.name != "router.home." || spec.qtype != 65400 || spec.qclass != classIN {
		t.Fatalf("unexpected custom query: %+v", spec)
	}
	if _, err := parseCustomQuery("router.home"); err == nil {
		t.Fatal("parseCustomQuery accepted a query without a type")
	}
}

func TestParseTransports(t *testing.T) {
	got, err := parseTransports("both,udp")
	if err != nil {
		t.Fatalf("parseTransports returned error: %v", err)
	}
	want := []transport{transportUDP, transportTCP}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseTransports = %v, want %v", got, want)
	}
	if _, err := parseTransports("icmp"); err == nil {
		t.Fatal("parseTransports accepted an unsupported transport")
	}
}
