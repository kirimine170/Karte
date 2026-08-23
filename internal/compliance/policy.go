package compliance

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
)

const SchemaVersion = 1

var licenseIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+-]*$`)
var sha256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// The parser is deliberately conservative. Add an exception only together
// with the SPDX-defined license combinations that may use it.
var supportedSPDXExceptions = map[string]map[string]struct{}{
	"GCC-exception-3.1": {
		"GPL-2.0-only": {}, "GPL-2.0-or-later": {},
		"GPL-3.0-only": {}, "GPL-3.0-or-later": {},
	},
}

type Policy struct {
	SchemaVersion                    int      `json:"schemaVersion"`
	AllowedLicenses                  []string `json:"allowedLicenses"`
	DeniedLicenseFamilies            []string `json:"deniedLicenseFamilies"`
	ExceptionRequiredLicenseFamilies []string `json:"exceptionRequiredLicenseFamilies"`
}

type ExceptionRegistry struct {
	SchemaVersion int                `json:"schemaVersion"`
	Exceptions    []LicenseException `json:"exceptions"`
}

type LicenseException struct {
	Component  string `json:"component"`
	Path       string `json:"path"`
	License    string `json:"license"`
	Reason     string `json:"reason"`
	ApprovedBy string `json:"approvedBy"`
	ExpiresAt  string `json:"expiresAt"`
}

type Component struct {
	ID               string            `json:"id"`
	Type             string            `json:"type"`
	Name             string            `json:"name"`
	Version          string            `json:"version,omitempty"`
	License          string            `json:"license"`
	Scope            string            `json:"scope"`
	Source           string            `json:"source,omitempty"`
	DistributionPath string            `json:"distributionPath,omitempty"`
	Hashes           map[string]string `json:"hashes,omitempty"`
	Properties       map[string]string `json:"properties,omitempty"`
	LicenseEvidence  []LicenseEvidence `json:"licenseEvidence,omitempty"`
}

type LicenseEvidence struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Text   string `json:"-"`
}

func (p Policy) Validate() error {
	if p.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported policy schemaVersion %d", p.SchemaVersion)
	}
	if len(p.AllowedLicenses) == 0 {
		return errors.New("allowedLicenses must not be empty")
	}
	for _, list := range [][]string{
		p.AllowedLicenses,
		p.DeniedLicenseFamilies,
		p.ExceptionRequiredLicenseFamilies,
	} {
		if err := validateUniqueNonEmpty(list); err != nil {
			return err
		}
	}
	return nil
}

func (r ExceptionRegistry) Validate(now time.Time) error {
	if r.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported exception schemaVersion %d", r.SchemaVersion)
	}
	seen := make(map[string]struct{}, len(r.Exceptions))
	for i, exception := range r.Exceptions {
		prefix := fmt.Sprintf("exceptions[%d]", i)
		for field, value := range map[string]string{
			"component":  exception.Component,
			"path":       exception.Path,
			"license":    exception.License,
			"reason":     exception.Reason,
			"approvedBy": exception.ApprovedBy,
			"expiresAt":  exception.ExpiresAt,
		} {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("%s.%s must not be empty", prefix, field)
			}
		}
		if !strings.HasPrefix(strings.ToUpper(exception.License), "LGPL-") {
			return fmt.Errorf("%s.license must be an LGPL SPDX identifier", prefix)
		}
		expiresAt, err := time.Parse("2006-01-02", exception.ExpiresAt)
		if err != nil {
			return fmt.Errorf("%s.expiresAt must be YYYY-MM-DD: %w", prefix, err)
		}
		if !expiresAt.After(dateOnlyUTC(now)) {
			return fmt.Errorf("%s expired on %s", prefix, exception.ExpiresAt)
		}
		key := exception.Component + "\x00" + exception.Path + "\x00" + exception.License
		if _, ok := seen[key]; ok {
			return fmt.Errorf("duplicate exception for component %q，path %q，license %q", exception.Component, exception.Path, exception.License)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func ValidateComponentLicenses(policy Policy, exceptions ExceptionRegistry, components []Component, now time.Time) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	if err := exceptions.Validate(now); err != nil {
		return err
	}

	allowed := stringSet(policy.AllowedLicenses)
	deniedFamilies := stringSet(policy.DeniedLicenseFamilies)
	exceptionFamilies := stringSet(policy.ExceptionRequiredLicenseFamilies)
	exceptionIndex := make(map[string]LicenseException, len(exceptions.Exceptions))
	for _, exception := range exceptions.Exceptions {
		exceptionIndex[exception.Component+"\x00"+exception.Path+"\x00"+exception.License] = exception
	}

	componentIDs := make(map[string]struct{}, len(components))
	consumedExceptions := make(map[string]struct{}, len(exceptions.Exceptions))
	var violations []string
	for _, component := range components {
		if err := validateComponent(component); err != nil {
			violations = append(violations, err.Error())
			continue
		}
		if _, ok := componentIDs[component.ID]; ok {
			violations = append(violations, fmt.Sprintf("duplicate component id %q", component.ID))
			continue
		}
		componentIDs[component.ID] = struct{}{}

		tokens, parseErr := parseLicenseExpression(component.License)
		if parseErr != nil {
			violations = append(violations, fmt.Sprintf("%s has malformed license expression %q: %v", component.ID, component.License, parseErr))
			continue
		}
		if len(tokens) == 0 {
			violations = append(violations, fmt.Sprintf("%s has missing or unknown license metadata", component.ID))
			continue
		}
		for _, token := range tokens {
			family := licenseFamily(token)
			if _, denied := deniedFamilies[family]; denied {
				violations = append(violations, fmt.Sprintf("%s uses denied license %s", component.ID, token))
				continue
			}
			if _, exceptionRequired := exceptionFamilies[family]; exceptionRequired {
				key := component.ID + "\x00" + component.DistributionPath + "\x00" + token
				if _, ok := exceptionIndex[key]; !ok {
					violations = append(violations, fmt.Sprintf("%s uses %s without an active path-specific exception", component.ID, token))
				} else {
					consumedExceptions[key] = struct{}{}
				}
				continue
			}
			if _, ok := allowed[token]; !ok {
				violations = append(violations, fmt.Sprintf("%s uses unapproved or unknown license %s", component.ID, token))
			}
		}
	}

	for _, exception := range exceptions.Exceptions {
		if _, ok := componentIDs[exception.Component]; !ok {
			violations = append(violations, fmt.Sprintf("exception references unknown component %q", exception.Component))
			continue
		}
		key := exception.Component + "\x00" + exception.Path + "\x00" + exception.License
		if _, ok := consumedExceptions[key]; !ok {
			violations = append(violations, fmt.Sprintf("exception for component %q，path %q，license %q is stale or unused", exception.Component, exception.Path, exception.License))
		}
	}
	if len(violations) == 0 {
		return nil
	}
	sort.Strings(violations)
	return errors.New(strings.Join(violations, "\n"))
}

func parseLicenseExpression(expression string) ([]string, error) {
	if strings.TrimSpace(expression) == "" || strings.EqualFold(strings.TrimSpace(expression), "NOASSERTION") {
		return nil, nil
	}
	tokens, err := tokenizeLicenseExpression(expression)
	if err != nil {
		return nil, err
	}
	parser := licenseExpressionParser{tokens: tokens}
	licenses, err := parser.parseExpression()
	if err != nil {
		return nil, err
	}
	if parser.position != len(tokens) {
		return nil, fmt.Errorf("unexpected token %q", tokens[parser.position])
	}
	seen := make(map[string]struct{}, len(licenses))
	unique := licenses[:0]
	for _, license := range licenses {
		if _, ok := seen[license]; ok {
			continue
		}
		seen[license] = struct{}{}
		unique = append(unique, license)
	}
	return unique, nil
}

type licenseExpressionParser struct {
	tokens   []string
	position int
}

func (p *licenseExpressionParser) parseExpression() ([]string, error) {
	licenses, err := p.parseAndExpression()
	if err != nil {
		return nil, err
	}
	for p.consume("OR") {
		right, err := p.parseAndExpression()
		if err != nil {
			return nil, err
		}
		licenses = append(licenses, right...)
	}
	return licenses, nil
}

func (p *licenseExpressionParser) parseAndExpression() ([]string, error) {
	licenses, err := p.parseWithExpression()
	if err != nil {
		return nil, err
	}
	for p.consume("AND") {
		right, err := p.parseWithExpression()
		if err != nil {
			return nil, err
		}
		licenses = append(licenses, right...)
	}
	return licenses, nil
}

func (p *licenseExpressionParser) parseWithExpression() ([]string, error) {
	licenses, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	if p.consume("WITH") {
		if len(licenses) != 1 {
			return nil, errors.New("WITH must apply to one license identifier")
		}
		if p.position >= len(p.tokens) || !licenseIdentifierPattern.MatchString(p.tokens[p.position]) || isLicenseOperator(p.tokens[p.position]) {
			return nil, errors.New("WITH is missing an exception identifier")
		}
		exception := p.tokens[p.position]
		p.position++
		allowedBases, known := supportedSPDXExceptions[exception]
		if !known {
			return nil, fmt.Errorf("unknown or unsupported SPDX exception %q", exception)
		}
		if _, compatible := allowedBases[licenses[0]]; !compatible {
			return nil, fmt.Errorf("SPDX exception %q does not apply to %q", exception, licenses[0])
		}
		licenses[0] += " WITH " + exception
	}
	return licenses, nil
}

func (p *licenseExpressionParser) parsePrimary() ([]string, error) {
	if p.position >= len(p.tokens) {
		return nil, errors.New("license identifier is missing")
	}
	token := p.tokens[p.position]
	if token == "(" {
		p.position++
		licenses, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		if !p.consume(")") {
			return nil, errors.New("closing parenthesis is missing")
		}
		return licenses, nil
	}
	if token == ")" || isLicenseOperator(token) || !licenseIdentifierPattern.MatchString(token) {
		return nil, fmt.Errorf("expected license identifier，got %q", token)
	}
	p.position++
	return []string{token}, nil
}

func (p *licenseExpressionParser) consume(want string) bool {
	if p.position >= len(p.tokens) || p.tokens[p.position] != want {
		return false
	}
	p.position++
	return true
}

func tokenizeLicenseExpression(expression string) ([]string, error) {
	var tokens []string
	for index := 0; index < len(expression); {
		if expression[index] == ' ' || expression[index] == '\t' || expression[index] == '\r' || expression[index] == '\n' {
			index++
			continue
		}
		if expression[index] == '(' || expression[index] == ')' {
			tokens = append(tokens, expression[index:index+1])
			index++
			continue
		}
		start := index
		for index < len(expression) && expression[index] != '(' && expression[index] != ')' && !strings.ContainsRune(" \t\r\n", rune(expression[index])) {
			index++
		}
		token := expression[start:index]
		if !licenseIdentifierPattern.MatchString(token) {
			return nil, fmt.Errorf("invalid character in token %q", token)
		}
		tokens = append(tokens, token)
	}
	if len(tokens) == 0 {
		return nil, errors.New("empty expression")
	}
	return tokens, nil
}

func isLicenseOperator(token string) bool {
	return token == "AND" || token == "OR" || token == "WITH"
}

func licenseFamily(identifier string) string {
	upper := strings.ToUpper(licenseBaseIdentifier(identifier))
	switch {
	case strings.HasPrefix(upper, "AGPL-"):
		return "AGPL"
	case strings.HasPrefix(upper, "LGPL-"):
		return "LGPL"
	case strings.HasPrefix(upper, "GPL-"):
		return "GPL"
	default:
		return identifier
	}
}

func licenseBaseIdentifier(term string) string {
	base, _, _ := strings.Cut(term, " WITH ")
	return base
}

func validateUniqueNonEmpty(values []string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return errors.New("policy license values must not be empty")
		}
		if _, ok := seen[value]; ok {
			return fmt.Errorf("duplicate policy license value %q", value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func dateOnlyUTC(value time.Time) time.Time {
	return time.Date(value.UTC().Year(), value.UTC().Month(), value.UTC().Day(), 0, 0, 0, 0, time.UTC)
}

func validateComponent(component Component) error {
	if strings.TrimSpace(component.ID) == "" {
		return errors.New("component id is missing")
	}
	for field, value := range map[string]string{
		"type":  component.Type,
		"name":  component.Name,
		"scope": component.Scope,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s has missing %s", component.ID, field)
		}
	}
	switch component.Type {
	case "go", "npm", "asset", "model", "native":
	default:
		return fmt.Errorf("%s has unsupported type %q", component.ID, component.Type)
	}
	switch component.Scope {
	case "runtime", "build", "optional", "system-runtime":
	default:
		return fmt.Errorf("%s has unsupported scope %q", component.ID, component.Scope)
	}
	if component.Source == "" {
		return fmt.Errorf("%s has missing source URL", component.ID)
	}
	parsedSource, err := url.Parse(component.Source)
	if err != nil || parsedSource.Scheme != "https" || parsedSource.Host == "" {
		return fmt.Errorf("%s has non-https or invalid source URL %q", component.ID, component.Source)
	}
	if component.DistributionPath != "" {
		if strings.Contains(component.DistributionPath, "\\") || strings.Contains(component.DistributionPath, ":") || strings.HasPrefix(component.DistributionPath, "/") || path.Clean(component.DistributionPath) != component.DistributionPath || component.DistributionPath == "." || strings.HasPrefix(component.DistributionPath, "../") {
			return fmt.Errorf("%s has non-portable or escaping distributionPath %q", component.ID, component.DistributionPath)
		}
	}
	if component.Type == "asset" || component.Type == "model" || component.Properties["artifactResolved"] == "true" {
		if !sha256Pattern.MatchString(component.Hashes["SHA-256"]) {
			return fmt.Errorf("%s has missing or invalid distributed SHA-256", component.ID)
		}
	}
	return nil
}
