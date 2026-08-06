package devicecatalog

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

//go:embed catalog.json
var catalogFiles embed.FS

const embeddedCatalogName = "catalog.json"

const (
	maxCatalogBytes            = 2 << 20
	maxDefinitions             = 256
	maxNameSignalsPerDevice    = 32
	maxDomainSignalsPerDevice  = 64
	maxAddressSignalsPerDevice = 16
	maxTokensPerNameSignal     = 12
)

type Catalog struct {
	SchemaVersion  int                  `json:"schema_version"`
	CatalogVersion string               `json:"catalog_version"`
	Confidence     ConfidenceThresholds `json:"confidence"`
	Definitions    []Definition         `json:"definitions"`
}

type ConfidenceThresholds struct {
	MediumScore int `json:"medium_score"`
	HighScore   int `json:"high_score"`
}

type Definition struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Category       string          `json:"category"`
	Icon           string          `json:"icon"`
	NameSignals    []NameSignal    `json:"name_signals,omitempty"`
	DomainSignals  []DomainSignal  `json:"domain_signals,omitempty"`
	AddressSignals []AddressSignal `json:"address_signals,omitempty"`
	Requirements   Requirements    `json:"requirements"`
}

type NameSignal struct {
	Tokens []string `json:"tokens"`
	Mode   string   `json:"mode"`
	Weight int      `json:"weight"`
}

type DomainSignal struct {
	Suffix string `json:"suffix"`
	Weight int    `json:"weight"`
}

type AddressSignal struct {
	Address string `json:"address"`
	Weight  int    `json:"weight"`
}

type Requirements struct {
	MinimumScore            int `json:"minimum_score"`
	MinimumDomainSignatures int `json:"minimum_domain_signatures,omitempty"`
}

type Evidence struct {
	Kind        string `json:"kind"`
	Value       string `json:"value"`
	Description string `json:"description"`
	Weight      int    `json:"weight"`
}

type Prediction struct {
	DefinitionID   string     `json:"definition_id"`
	DeviceType     string     `json:"device_type"`
	Category       string     `json:"category"`
	Icon           string     `json:"icon"`
	Confidence     string     `json:"confidence"`
	Score          int        `json:"score"`
	CatalogVersion string     `json:"catalog_version"`
	SignalHash     string     `json:"-"`
	Evidence       []Evidence `json:"evidence"`
	EvaluatedAt    string     `json:"evaluated_at"`
}

type predictionCandidate struct {
	definition Definition
	score      int
	evidence   []Evidence
	domains    int
	nameMatch  bool
}

type Info struct {
	SchemaVersion  int    `json:"schema_version"`
	CatalogVersion string `json:"catalog_version"`
	Source         string `json:"source"`
	Definitions    int    `json:"definitions"`
	LastError      string `json:"last_error,omitempty"`
	ExternalPath   string `json:"external_path,omitempty"`
}

type Manager struct {
	mu            sync.Mutex
	path          string
	embedded      Catalog
	current       Catalog
	source        string
	lastError     string
	lastModified  time.Time
	lastSize      int64
	lastChecked   time.Time
	checkInterval time.Duration
}

func NewManager(path string) *Manager {
	baseline, err := loadEmbedded()
	if err != nil {
		panic(fmt.Sprintf("load embedded device catalog: %v", err))
	}
	manager := &Manager{
		path:          strings.TrimSpace(path),
		embedded:      baseline,
		current:       baseline,
		source:        "embedded",
		checkInterval: 2 * time.Second,
	}
	manager.refresh(true)
	return manager
}

func Parse(data []byte) (Catalog, error) {
	var catalog Catalog
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&catalog); err != nil {
		return Catalog{}, fmt.Errorf("decode device catalog: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Catalog{}, err
	}
	if err := Validate(catalog); err != nil {
		return Catalog{}, err
	}
	return catalog, nil
}

func Validate(catalog Catalog) error {
	if catalog.SchemaVersion != 1 {
		return fmt.Errorf("unsupported device catalog schema version %d", catalog.SchemaVersion)
	}
	if strings.TrimSpace(catalog.CatalogVersion) == "" {
		return errors.New("device catalog version is required")
	}
	if len(catalog.CatalogVersion) > 64 {
		return errors.New("device catalog version is too long")
	}
	if catalog.Confidence.MediumScore < 1 || catalog.Confidence.HighScore < catalog.Confidence.MediumScore {
		return errors.New("device catalog confidence thresholds are invalid")
	}
	if len(catalog.Definitions) == 0 {
		return errors.New("device catalog has no definitions")
	}
	if len(catalog.Definitions) > maxDefinitions {
		return fmt.Errorf("device catalog has more than %d definitions", maxDefinitions)
	}

	ids := map[string]bool{}
	for index, definition := range catalog.Definitions {
		if err := validateDefinition(index, definition, ids); err != nil {
			return err
		}
	}
	return nil
}

func validateDefinition(index int, definition Definition, ids map[string]bool) error {
	prefix := fmt.Sprintf("definition %d", index+1)
	if !validID(definition.ID) {
		return fmt.Errorf("%s has invalid id %q", prefix, definition.ID)
	}
	if ids[definition.ID] {
		return fmt.Errorf("duplicate device definition id %q", definition.ID)
	}
	ids[definition.ID] = true
	if strings.TrimSpace(definition.Name) == "" || strings.TrimSpace(definition.Category) == "" || strings.TrimSpace(definition.Icon) == "" {
		return fmt.Errorf("%s must define name, category, and icon", definition.ID)
	}
	if len(definition.Name) > 64 || len(definition.Category) > 64 || len(definition.Icon) > 64 {
		return fmt.Errorf("%s has display metadata that is too long", definition.ID)
	}
	if definition.Requirements.MinimumScore < 1 {
		return fmt.Errorf("%s has invalid minimum score", definition.ID)
	}
	if len(definition.NameSignals)+len(definition.DomainSignals)+len(definition.AddressSignals) == 0 {
		return fmt.Errorf("%s has no recognition signals", definition.ID)
	}
	if len(definition.NameSignals) > maxNameSignalsPerDevice ||
		len(definition.DomainSignals) > maxDomainSignalsPerDevice ||
		len(definition.AddressSignals) > maxAddressSignalsPerDevice {
		return fmt.Errorf("%s defines too many recognition signals", definition.ID)
	}
	if err := validateNameSignals(definition); err != nil {
		return err
	}
	if err := validateDomainSignals(definition); err != nil {
		return err
	}
	return validateAddressSignals(definition)
}

func validateNameSignals(definition Definition) error {
	for _, signal := range definition.NameSignals {
		if signal.Mode != "any" && signal.Mode != "all" {
			return fmt.Errorf("%s has unsupported name signal mode %q", definition.ID, signal.Mode)
		}
		if len(signal.Tokens) == 0 || len(signal.Tokens) > maxTokensPerNameSignal || !validWeight(signal.Weight) {
			return fmt.Errorf("%s has invalid name signal", definition.ID)
		}
		for _, token := range signal.Tokens {
			if !validToken(token) {
				return fmt.Errorf("%s has invalid hostname token %q", definition.ID, token)
			}
		}
	}
	return nil
}

func validateDomainSignals(definition Definition) error {
	seenDomains := map[string]bool{}
	for _, signal := range definition.DomainSignals {
		suffix := normalizeDomain(signal.Suffix)
		if !validDomain(suffix) || !validWeight(signal.Weight) {
			return fmt.Errorf("%s has invalid domain signal %q", definition.ID, signal.Suffix)
		}
		if seenDomains[suffix] {
			return fmt.Errorf("%s repeats domain signal %q", definition.ID, suffix)
		}
		seenDomains[suffix] = true
	}
	if definition.Requirements.MinimumDomainSignatures > len(definition.DomainSignals) {
		return fmt.Errorf("%s requires more domain signatures than it defines", definition.ID)
	}
	return nil
}

func validateAddressSignals(definition Definition) error {
	for _, signal := range definition.AddressSignals {
		if net.ParseIP(strings.TrimSpace(signal.Address)) == nil || !validWeight(signal.Weight) {
			return fmt.Errorf("%s has invalid address signal %q", definition.ID, signal.Address)
		}
	}
	return nil
}

func (m *Manager) Predict(name, address string, domains []string) Prediction {
	m.refresh(false)
	m.mu.Lock()
	catalog := m.current
	m.mu.Unlock()
	return catalog.Predict(name, address, domains)
}

func (m *Manager) Info() Info {
	m.refresh(false)
	m.mu.Lock()
	defer m.mu.Unlock()
	return Info{
		SchemaVersion:  m.current.SchemaVersion,
		CatalogVersion: m.current.CatalogVersion,
		Source:         m.source,
		Definitions:    len(m.current.Definitions),
		LastError:      m.lastError,
		ExternalPath:   m.path,
	}
}

func (catalog Catalog) Predict(name, address string, domains []string) Prediction {
	nameTokens := signalTokens(name)
	normalizedAddress := strings.TrimSpace(address)
	normalizedDomains := normalizeDomains(domains)
	candidates := collectCandidates(catalog.Definitions, nameTokens, normalizedAddress, normalizedDomains)

	now := time.Now().UTC().Format(time.RFC3339)
	inputHash := signalHash(catalog.CatalogVersion, name, normalizedAddress, normalizedDomains)
	if len(candidates) == 0 {
		return unknownPrediction(catalog.CatalogVersion, inputHash, now, nil)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			return candidates[i].definition.ID < candidates[j].definition.ID
		}
		return candidates[i].score > candidates[j].score
	})
	if len(candidates) > 1 && candidates[0].score == candidates[1].score {
		return unknownPrediction(catalog.CatalogVersion, inputHash, now, []Evidence{{
			Kind:        "conflict",
			Value:       candidates[0].definition.ID + "," + candidates[1].definition.ID,
			Description: "The strongest catalog matches were tied",
		}})
	}

	best := candidates[0]
	confidence := "medium"
	if best.score >= catalog.Confidence.HighScore {
		confidence = "high"
	} else if best.score < catalog.Confidence.MediumScore {
		confidence = "unknown"
	}
	return Prediction{
		DefinitionID:   best.definition.ID,
		DeviceType:     best.definition.Name,
		Category:       best.definition.Category,
		Icon:           best.definition.Icon,
		Confidence:     confidence,
		Score:          best.score,
		CatalogVersion: catalog.CatalogVersion,
		SignalHash:     inputHash,
		Evidence:       best.evidence,
		EvaluatedAt:    now,
	}
}

func collectCandidates(definitions []Definition, nameTokens map[string]bool, address string, domains []string) []predictionCandidate {
	candidates := make([]predictionCandidate, 0, len(definitions))
	for _, definition := range definitions {
		current := scoreDefinition(definition, nameTokens, address, domains)
		if !current.nameMatch && current.domains < definition.Requirements.MinimumDomainSignatures {
			continue
		}
		if current.score >= definition.Requirements.MinimumScore {
			candidates = append(candidates, current)
		}
	}
	return candidates
}

func scoreDefinition(definition Definition, nameTokens map[string]bool, address string, domains []string) predictionCandidate {
	candidate := predictionCandidate{definition: definition}
	addNameEvidence(&candidate, nameTokens)
	addDomainEvidence(&candidate, domains)
	addAddressEvidence(&candidate, address)
	return candidate
}

func addNameEvidence(candidate *predictionCandidate, tokens map[string]bool) {
	for _, signal := range candidate.definition.NameSignals {
		matched, value := matchNameSignal(tokens, signal)
		if !matched {
			continue
		}
		candidate.nameMatch = true
		candidate.score += signal.Weight
		candidate.evidence = append(candidate.evidence, Evidence{
			Kind:        "hostname",
			Value:       value,
			Description: fmt.Sprintf("Device name contained %s", humanList(strings.Split(value, ","))),
			Weight:      signal.Weight,
		})
	}
}

func addDomainEvidence(candidate *predictionCandidate, domains []string) {
	for _, signal := range candidate.definition.DomainSignals {
		for _, domain := range domains {
			if !domainMatches(domain, signal.Suffix) {
				continue
			}
			candidate.domains++
			candidate.score += signal.Weight
			candidate.evidence = append(candidate.evidence, Evidence{
				Kind:        "domain",
				Value:       normalizeDomain(signal.Suffix),
				Description: fmt.Sprintf("Queried the %s domain family", normalizeDomain(signal.Suffix)),
				Weight:      signal.Weight,
			})
			break
		}
	}
}

func addAddressEvidence(candidate *predictionCandidate, address string) {
	for _, signal := range candidate.definition.AddressSignals {
		if address != strings.TrimSpace(signal.Address) {
			continue
		}
		candidate.score += signal.Weight
		candidate.evidence = append(candidate.evidence, Evidence{
			Kind:        "address",
			Value:       address,
			Description: "Matched a reserved local address",
			Weight:      signal.Weight,
		})
	}
}

func unknownPrediction(catalogVersion, signalHashValue, evaluatedAt string, evidence []Evidence) Prediction {
	if evidence == nil {
		evidence = make([]Evidence, 0)
	}
	return Prediction{
		DeviceType:     "Unknown",
		Category:       "unknown",
		Icon:           "monitor",
		Confidence:     "unknown",
		CatalogVersion: catalogVersion,
		SignalHash:     signalHashValue,
		Evidence:       evidence,
		EvaluatedAt:    evaluatedAt,
	}
}

func (m *Manager) refresh(force bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.path == "" {
		return
	}
	now := time.Now()
	if !force && now.Sub(m.lastChecked) < m.checkInterval {
		return
	}
	m.lastChecked = now
	info, err := os.Stat(m.path)
	if err != nil {
		if os.IsNotExist(err) {
			if m.source == "external" {
				m.current = m.embedded
				m.source = "embedded"
				m.lastModified = time.Time{}
				m.lastSize = 0
			}
			m.lastError = ""
			return
		}
		m.lastError = err.Error()
		return
	}
	if info.Size() > maxCatalogBytes {
		m.lastError = fmt.Sprintf("device catalog exceeds the %d byte limit", maxCatalogBytes)
		return
	}
	if !force && m.source == "external" && info.ModTime().Equal(m.lastModified) && info.Size() == m.lastSize {
		return
	}
	data, err := os.ReadFile(m.path)
	if err != nil {
		m.lastError = err.Error()
		return
	}
	catalog, err := Parse(data)
	if err != nil {
		m.lastError = err.Error()
		return
	}
	m.current = catalog
	m.source = "external"
	m.lastModified = info.ModTime()
	m.lastSize = info.Size()
	m.lastError = ""
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("device catalog contains multiple JSON values")
		}
		return fmt.Errorf("decode trailing device catalog data: %w", err)
	}
	return nil
}

func loadEmbedded() (Catalog, error) {
	data, err := catalogFiles.ReadFile(embeddedCatalogName)
	if err != nil {
		return Catalog{}, err
	}
	return Parse(data)
}

func matchNameSignal(tokens map[string]bool, signal NameSignal) (bool, string) {
	matched := make([]string, 0, len(signal.Tokens))
	for _, token := range signal.Tokens {
		normalized := strings.ToLower(strings.TrimSpace(token))
		if tokens[normalized] {
			matched = append(matched, normalized)
		}
	}
	if signal.Mode == "all" && len(matched) != len(signal.Tokens) {
		return false, ""
	}
	if signal.Mode == "any" && len(matched) == 0 {
		return false, ""
	}
	return true, strings.Join(matched, ",")
}

func signalTokens(value string) map[string]bool {
	tokens := map[string]bool{}
	for _, token := range strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	}) {
		if token != "" {
			tokens[token] = true
		}
	}
	return tokens
}

func normalizeDomains(domains []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(domains))
	for _, domain := range domains {
		normalized := normalizeDomain(domain)
		if normalized == "" || seen[normalized] {
			continue
		}
		seen[normalized] = true
		result = append(result, normalized)
	}
	sort.Strings(result)
	return result
}

func normalizeDomain(domain string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
}

func domainMatches(domain, suffix string) bool {
	suffix = normalizeDomain(suffix)
	return domain == suffix || strings.HasSuffix(domain, "."+suffix)
}

func validDomain(domain string) bool {
	if len(domain) < 3 || len(domain) > 253 || !strings.Contains(domain, ".") || strings.ContainsAny(domain, " /*?") {
		return false
	}
	for _, label := range strings.Split(domain, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
				return false
			}
		}
	}
	return true
}

func validID(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
			return false
		}
	}
	return true
}

func validToken(value string) bool {
	if value == "" || value != strings.ToLower(value) {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') {
			return false
		}
	}
	return true
}

func validWeight(weight int) bool {
	return weight >= 1 && weight <= 20
}

func signalHash(version, name, address string, domains []string) string {
	payload, _ := json.Marshal(struct {
		Version string   `json:"version"`
		Name    string   `json:"name"`
		Address string   `json:"address"`
		Domains []string `json:"domains"`
	}{
		Version: version,
		Name:    strings.ToLower(strings.TrimSpace(name)),
		Address: strings.TrimSpace(address),
		Domains: domains,
	})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func humanList(values []string) string {
	if len(values) == 1 {
		return values[0]
	}
	return strings.Join(values, " + ")
}
