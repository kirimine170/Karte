package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

const securityThreatModelPath = "security/threat-model.json"

type securityThreatModel struct {
	SchemaVersion    int                 `json:"schemaVersion"`
	RequiredSurfaces []string            `json:"requiredSurfaces"`
	Surfaces         []securityNamedItem `json:"surfaces"`
	Capabilities     []securityNamedItem `json:"capabilities"`
	Assets           []securityNamedItem `json:"assets"`
	Actors           []securityActor     `json:"actors"`
	Boundaries       []securityBoundary  `json:"boundaries"`
	Flows            []securityFlow      `json:"flows"`
	Controls         []securityControl   `json:"controls"`
	Threats          []securityThreat    `json:"threats"`
	IPCMethods       []securityIPCMethod `json:"ipcMethods"`
}

type securityNamedItem struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

type securityActor struct {
	ID           string   `json:"id"`
	Description  string   `json:"description"`
	Capabilities []string `json:"capabilities"`
}

type securityBoundary struct {
	ID                 string                `json:"id"`
	Description        string                `json:"description"`
	ProductionEvidence []securityEvidenceRef `json:"productionEvidence"`
}

type securityFlow struct {
	ID                 string                `json:"id"`
	Description        string                `json:"description"`
	Source             string                `json:"source"`
	Processors         []string              `json:"processors"`
	Sink               string                `json:"sink"`
	SurfaceIDs         []string              `json:"surfaceIds"`
	BoundaryIDs        []string              `json:"boundaryIds"`
	ProductionEvidence []securityEvidenceRef `json:"productionEvidence"`
}

type securityControl struct {
	ID                 string                `json:"id"`
	Description        string                `json:"description"`
	Severity           string                `json:"severity"`
	Status             string                `json:"status"`
	Owner              string                `json:"owner"`
	ClosureTask        string                `json:"closureTask"`
	SurfaceIDs         []string              `json:"surfaceIds"`
	ProductionEvidence []securityEvidenceRef `json:"productionEvidence"`
	TestEvidence       []securityEvidenceRef `json:"testEvidence"`
}

type securityThreat struct {
	ID                 string                `json:"id"`
	Description        string                `json:"description"`
	Severity           string                `json:"severity"`
	Status             string                `json:"status"`
	Owner              string                `json:"owner"`
	ClosureTask        string                `json:"closureTask"`
	ResidualRisk       string                `json:"residualRisk"`
	ResidualRiskLevel  string                `json:"residualRiskLevel"`
	SurfaceIDs         []string              `json:"surfaceIds"`
	AssetIDs           []string              `json:"assetIds"`
	ActorIDs           []string              `json:"actorIds"`
	FlowIDs            []string              `json:"flowIds"`
	ControlIDs         []string              `json:"controlIds"`
	ProductionEvidence []securityEvidenceRef `json:"productionEvidence"`
	TestEvidence       []securityEvidenceRef `json:"testEvidence"`
}

type securityIPCMethod struct {
	Name         string   `json:"name"`
	SurfaceIDs   []string `json:"surfaceIds"`
	Capabilities []string `json:"capabilities"`
}

type securityEvidenceRef struct {
	Path   string `json:"path"`
	Symbol string `json:"symbol"`
}

func TestSecurityThreatModelInventory(t *testing.T) {
	model := loadSecurityThreatModel(t)
	if model.SchemaVersion != 1 {
		t.Fatalf("schemaVersion = %d，want 1", model.SchemaVersion)
	}

	surfaces := securityNamedIDs(t, "surface", model.Surfaces)
	capabilities := securityNamedIDs(t, "capability", model.Capabilities)
	assets := securityNamedIDs(t, "asset", model.Assets)
	actors := securityActorIDs(t, model.Actors, capabilities)
	boundaries := securityBoundaryIDs(t, model.Boundaries)
	flows := securityFlowIDs(t, model.Flows, surfaces, boundaries)
	controls := securityControlIDs(t, model.Controls, surfaces)
	threats := securityThreatIDs(t, model.Threats, surfaces, assets, actors, flows, controls)

	coveredSurfaces := make(map[string]bool)
	for _, flow := range model.Flows {
		markSecurityReferences(coveredSurfaces, flow.SurfaceIDs)
	}
	for _, control := range model.Controls {
		markSecurityReferences(coveredSurfaces, control.SurfaceIDs)
	}
	for _, threat := range model.Threats {
		markSecurityReferences(coveredSurfaces, threat.SurfaceIDs)
	}
	for _, required := range model.RequiredSurfaces {
		requireSecurityReference(t, "required surface", required, surfaces)
		if !coveredSurfaces[required] {
			t.Errorf("required surface %q is not covered by a flow，control，or threat", required)
		}
	}

	validateSecurityIPCInventory(t, model.IPCMethods, surfaces, capabilities)
	if len(threats) == 0 {
		t.Fatal("threat inventory is empty")
	}
}

func TestSecurityThreatModelKnownBlockers(t *testing.T) {
	model := loadSecurityThreatModel(t)

	requireBlockedSecurityThreat(t, model, "TM-MEDIA-PARSER-002")
	mediaFinalizeCalls := securityGoFunctionCalls(t, "app_media_import.go", "finishSingle")
	requireSecurityCall(t, mediaFinalizeCalls, "validateMediaFileMagic")
	rejectSecurityCall(t, mediaFinalizeCalls, "DecodeToPCM")

	requireBlockedSecurityThreat(t, model, "TM-PREVIEW-001")
	previewIframe := securityHTMLElementByID(t, "frontend/index.html", "iframe", "preview")
	if _, exists := securityHTMLAttribute(previewIframe, "sandbox"); exists {
		t.Fatal("preview iframe now has a sandbox attribute，so TM-PREVIEW-001 evidence and status must be reviewed before this gate is updated")
	}
	previewCalls := securityGoFunctionCalls(t, "app.go", "PreviewMarkdownForPath")
	requireSecurityCall(t, previewCalls, "karterenderer.RenderString")
	securityRequireSourcePattern(t, "frontend/src/utils/preview-frame.ts", `doc\s*\.\s*write\s*\(\s*stripLegacyRemotePreviewAssets\s*\(\s*html\s*\)\s*\)`)

	requireBlockedSecurityThreat(t, model, "TM-THEME-001")
	securityRequireSourcePattern(t, "frontend/src/utils/custom-css.ts", `<style[^>]*karte-custom-css[^>]*>\$\{cssToInject\}</style>`)
	securityRequireSourcePattern(t, "frontend/src/utils/preview-frame.ts", `securityLevel\s*:\s*['"]loose['"]`)

	requireBlockedSecurityThreat(t, model, "TM-PDF-LOCAL-001")
	if !securityGoFunctionHasTrueField(t, "app.go", "exportHTMLToPDFWithRenderer", "AllowLocalFiles") {
		t.Fatal("PDF adapter no longer sets AllowLocalFiles=true，so TM-PDF-LOCAL-001 evidence and status must be reviewed")
	}
	imageCalls := securityGoFunctionCalls(t, "app.go", "convertImageURLsToDataURIs")
	requireSecurityCall(t, imageCalls, "os.Stat")
	requireSecurityCall(t, imageCalls, "os.ReadFile")
	rejectSecurityCall(t, imageCalls, "openConfinedMediaFile")

	requireBlockedSecurityThreat(t, model, "TM-PDF-NETWORK-002")
	securityRequireGoFunctionString(t, "app.go", "ensurePDFRenderAssets", "https://cdn.jsdelivr.net/")
	securityRequireGoFunctionString(t, "app.go", "ensurePDFRenderAssets", "@latest")

	requireBlockedSecurityThreat(t, model, "TM-IPC-FS-001")
	if !securityGoFunctionFieldContainsIdentifier(t, "main.go", "newWailsAppOptions", "Bind", "app") {
		t.Fatal("Wails no longer binds the whole App value，so TM-IPC-FS-001 evidence and status must be reviewed")
	}
	contentCalls := securityGoFunctionCalls(t, "app.go", "resolveContentPath")
	rejectSecurityCall(t, contentCalls, "filepath.EvalSymlinks")
	rejectSecurityCall(t, contentCalls, "os.OpenRoot")

	requireBlockedSecurityThreat(t, model, "TM-SITE-FS-001")
	siteScanCalls := securityGoFunctionCalls(t, "site_build.go", "scanSiteBuildSources")
	requireSecurityCall(t, siteScanCalls, "os.ReadFile")
	rejectSecurityCall(t, siteScanCalls, "os.OpenRoot")
	requireBlockedSecurityThreat(t, model, "TM-SITE-CONTENT-002")
	siteRendererCalls := securityGoFunctionCalls(t, "site_build.go", "newSiteBuilder")
	requireSecurityCall(t, siteRendererCalls, "karterenderer.RenderMarkdown")
	rejectSecurityCall(t, siteRendererCalls, "sanitizeHTMLForMarkdown")

	requireBlockedSecurityThreat(t, model, "TM-BINARY-001")
	ffmpegCalls := securityGoFunctionCalls(t, "internal/audio/decoder.go", "findFFmpeg")
	requireSecurityCall(t, ffmpegCalls, "findFFmpegForOS")
	ffmpegPolicyCalls := securityGoFunctionCalls(t, "internal/audio/decoder.go", "findFFmpegForOS")
	requireSecurityCall(t, ffmpegPolicyCalls, "getenv")
	requireSecurityCall(t, ffmpegPolicyCalls, "lookPath")
	securityRequireGoFunctionString(t, "internal/audio/decoder.go", "findFFmpegForOS", "KARTE_FFMPEG_BINARY")
	securityRequireGoFunctionString(t, "internal/audio/decoder.go", "findFFmpegForOS", "FFMPEG_PATH")

	requireBlockedSecurityThreat(t, model, "TM-NATIVE-MODEL-001")
	modelPathCalls := securityGoFunctionCalls(t, "internal/asr/config.go", "EnsureModelPathsAbsolute")
	requireSecurityCall(t, modelPathCalls, "filepath.IsAbs")
	rejectSecurityCall(t, modelPathCalls, "filepath.EvalSymlinks")
	rejectSecurityCall(t, modelPathCalls, "os.OpenRoot")
	modelVerifyCalls := securityGoFunctionCalls(t, "internal/asr/realtime.go", "verifyModelFiles")
	requireSecurityCall(t, modelVerifyCalls, "os.Stat")
	rejectSecurityCall(t, modelVerifyCalls, "os.Lstat")

	requireBlockedSecurityThreat(t, model, "TM-ARTIFACT-001")
	workflow, err := os.ReadFile(".github/workflows/ci.yml")
	if err != nil {
		t.Fatalf("read desktop workflow: %v", err)
	}
	artifactAuditCommand := regexp.MustCompile(`(?m)^\s*(?:go\s+run\s+\./cmd/licensegate|(?:\./)?licensegate)\b[^\n#]*\bartifact-audit\b`)
	if artifactAuditCommand.Match(workflow) {
		t.Fatal("desktop workflow now invokes artifact-audit，so TM-ARTIFACT-001 evidence and status must be reviewed")
	}
	requireBlockedSecurityThreat(t, model, "TM-ARTIFACT-SUPPLY-002")
	securityRequireSourcePattern(t, ".github/workflows/ci.yml", `(?m)^\s*uses:\s*[^\s#]+@v[0-9]+\s*$`)
}

func loadSecurityThreatModel(t *testing.T) securityThreatModel {
	t.Helper()
	file, err := os.Open(securityThreatModelPath)
	if err != nil {
		t.Fatalf("open threat model: %v", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var model securityThreatModel
	if err := decoder.Decode(&model); err != nil {
		t.Fatalf("decode threat model: %v", err)
	}
	if err := ensureSecurityJSONEOF(decoder); err != nil {
		t.Fatalf("decode threat model: %v", err)
	}
	return model
}

func ensureSecurityJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}

func securityNamedIDs(t *testing.T, kind string, items []securityNamedItem) map[string]bool {
	t.Helper()
	ids := make(map[string]bool, len(items))
	for _, item := range items {
		requireSecurityText(t, kind+" id", item.ID)
		requireSecurityText(t, kind+" description", item.Description)
		if ids[item.ID] {
			t.Errorf("duplicate %s id %q", kind, item.ID)
		}
		ids[item.ID] = true
	}
	return ids
}

func securityActorIDs(t *testing.T, actors []securityActor, capabilities map[string]bool) map[string]bool {
	t.Helper()
	ids := make(map[string]bool, len(actors))
	for _, actor := range actors {
		requireSecurityText(t, "actor id", actor.ID)
		requireSecurityText(t, "actor description", actor.Description)
		if ids[actor.ID] {
			t.Errorf("duplicate actor id %q", actor.ID)
		}
		ids[actor.ID] = true
		if len(actor.Capabilities) == 0 {
			t.Errorf("actor %s has no capabilities", actor.ID)
		}
		for _, capability := range actor.Capabilities {
			requireSecurityReference(t, "actor capability", capability, capabilities)
		}
	}
	return ids
}

func securityBoundaryIDs(t *testing.T, boundaries []securityBoundary) map[string]bool {
	t.Helper()
	ids := make(map[string]bool, len(boundaries))
	for _, boundary := range boundaries {
		requireSecurityText(t, "boundary id", boundary.ID)
		requireSecurityText(t, "boundary description", boundary.Description)
		if ids[boundary.ID] {
			t.Errorf("duplicate boundary id %q", boundary.ID)
		}
		ids[boundary.ID] = true
		validateSecurityEvidence(t, "boundary "+boundary.ID+" production", boundary.ProductionEvidence, false)
	}
	return ids
}

func securityFlowIDs(t *testing.T, flows []securityFlow, surfaces, boundaries map[string]bool) map[string]bool {
	t.Helper()
	ids := make(map[string]bool, len(flows))
	for _, flow := range flows {
		requireSecurityText(t, "flow id", flow.ID)
		requireSecurityText(t, "flow description", flow.Description)
		requireSecurityText(t, "flow source", flow.Source)
		requireSecurityText(t, "flow sink", flow.Sink)
		if len(flow.Processors) == 0 {
			t.Errorf("flow %s has no processors", flow.ID)
		}
		if ids[flow.ID] {
			t.Errorf("duplicate flow id %q", flow.ID)
		}
		ids[flow.ID] = true
		validateSecurityReferences(t, "flow surface", flow.SurfaceIDs, surfaces)
		validateSecurityReferences(t, "flow boundary", flow.BoundaryIDs, boundaries)
		validateSecurityEvidence(t, "flow "+flow.ID+" production", flow.ProductionEvidence, false)
	}
	return ids
}

func securityControlIDs(t *testing.T, controls []securityControl, surfaces map[string]bool) map[string]bool {
	t.Helper()
	ids := make(map[string]bool, len(controls))
	for _, control := range controls {
		validateSecurityGovernanceFields(t, "control "+control.ID, control.ID, control.Description, control.Severity, control.Status, control.Owner, control.ClosureTask)
		if ids[control.ID] {
			t.Errorf("duplicate control id %q", control.ID)
		}
		ids[control.ID] = true
		validateSecurityReferences(t, "control surface", control.SurfaceIDs, surfaces)
		validateSecurityEvidence(t, "control "+control.ID+" production", control.ProductionEvidence, false)
		validateSecurityEvidence(t, "control "+control.ID+" test", control.TestEvidence, true)
		if control.Status == "implemented" && (len(control.ProductionEvidence) == 0 || len(control.TestEvidence) == 0) {
			t.Errorf("implemented control %s requires production and test evidence", control.ID)
		}
		if control.Status == "blocked" {
			requireConcreteSecurityClosure(t, "control "+control.ID, control.ClosureTask)
		}
	}
	return ids
}

func securityThreatIDs(t *testing.T, threats []securityThreat, surfaces, assets, actors, flows, controls map[string]bool) map[string]bool {
	t.Helper()
	ids := make(map[string]bool, len(threats))
	for _, threat := range threats {
		validateSecurityGovernanceFields(t, "threat "+threat.ID, threat.ID, threat.Description, threat.Severity, threat.Status, threat.Owner, threat.ClosureTask)
		if ids[threat.ID] {
			t.Errorf("duplicate threat id %q", threat.ID)
		}
		ids[threat.ID] = true
		requireSecurityText(t, "threat residual risk", threat.ResidualRisk)
		if !securityAllowedValue(threat.ResidualRiskLevel, "none", "low", "medium", "high", "critical") {
			t.Errorf("threat %s has invalid residualRiskLevel %q", threat.ID, threat.ResidualRiskLevel)
		}
		validateSecurityReferences(t, "threat surface", threat.SurfaceIDs, surfaces)
		validateSecurityReferences(t, "threat asset", threat.AssetIDs, assets)
		validateSecurityReferences(t, "threat actor", threat.ActorIDs, actors)
		validateSecurityReferences(t, "threat flow", threat.FlowIDs, flows)
		validateSecurityReferences(t, "threat control", threat.ControlIDs, controls)
		validateSecurityEvidence(t, "threat "+threat.ID+" production", threat.ProductionEvidence, false)
		validateSecurityEvidence(t, "threat "+threat.ID+" test", threat.TestEvidence, true)
		if len(threat.ProductionEvidence) == 0 || len(threat.TestEvidence) == 0 {
			t.Errorf("threat %s requires production and test evidence", threat.ID)
		}
		if threat.Status == "implemented" && !securityAllowedValue(threat.ResidualRiskLevel, "none", "low") {
			t.Errorf("implemented threat %s retains %s residual risk", threat.ID, threat.ResidualRiskLevel)
		}
		if securityAllowedValue(threat.Severity, "critical", "high") && !securityAllowedValue(threat.ResidualRiskLevel, "none", "low") {
			if threat.Status != "blocked" {
				t.Errorf("%s threat %s with %s residual risk must be blocked", threat.Severity, threat.ID, threat.ResidualRiskLevel)
			}
			requireConcreteSecurityClosure(t, "threat "+threat.ID, threat.ClosureTask)
		}
	}
	return ids
}

func validateSecurityGovernanceFields(t *testing.T, label, id, description, severity, status, owner, closureTask string) {
	t.Helper()
	requireSecurityText(t, label+" id", id)
	requireSecurityText(t, label+" description", description)
	requireSecurityText(t, label+" owner", owner)
	requireSecurityText(t, label+" closureTask", closureTask)
	if !securityAllowedValue(severity, "critical", "high", "medium", "low") {
		t.Errorf("%s has invalid severity %q", label, severity)
	}
	if !securityAllowedValue(status, "implemented", "partial", "blocked") {
		t.Errorf("%s has invalid status %q", label, status)
	}
}

func validateSecurityIPCInventory(t *testing.T, inventory []securityIPCMethod, surfaces, capabilities map[string]bool) {
	t.Helper()
	inventoryNames := make(map[string]bool, len(inventory))
	previous := ""
	for _, method := range inventory {
		if method.Name <= previous {
			t.Errorf("ipcMethods must be strictly sorted，found %q after %q", method.Name, previous)
		}
		previous = method.Name
		if inventoryNames[method.Name] {
			t.Errorf("duplicate IPC method %q", method.Name)
		}
		inventoryNames[method.Name] = true
		validateSecurityReferences(t, "IPC method surface", method.SurfaceIDs, surfaces)
		validateSecurityReferences(t, "IPC method capability", method.Capabilities, capabilities)
	}

	productionMethods := exportedAppMethods(t)
	var missing, stale []string
	for method := range productionMethods {
		if !inventoryNames[method] {
			missing = append(missing, method)
		}
	}
	for method := range inventoryNames {
		if !productionMethods[method] {
			stale = append(stale, method)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) > 0 || len(stale) > 0 {
		t.Errorf("App IPC inventory drift，unclassified exported methods=%v，stale methods=%v", missing, stale)
	}
}

func exportedAppMethods(t *testing.T) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("list repository root: %v", err)
	}
	methods := make(map[string]bool)
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv == nil || !function.Name.IsExported() || len(function.Recv.List) != 1 {
				continue
			}
			if securityReceiverName(function.Recv.List[0].Type) == "App" {
				methods[function.Name.Name] = true
			}
		}
	}
	return methods
}

func securityReceiverName(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.StarExpr:
		return securityReceiverName(value.X)
	case *ast.IndexExpr:
		return securityReceiverName(value.X)
	case *ast.IndexListExpr:
		return securityReceiverName(value.X)
	default:
		return ""
	}
}

func validateSecurityEvidence(t *testing.T, label string, refs []securityEvidenceRef, testEvidence bool) {
	t.Helper()
	if len(refs) == 0 {
		t.Errorf("%s evidence is empty", label)
		return
	}
	for _, ref := range refs {
		if filepath.IsAbs(ref.Path) || filepath.Clean(ref.Path) != ref.Path || ref.Path == "." || strings.HasPrefix(filepath.ToSlash(ref.Path), "../") {
			t.Errorf("%s has unsafe evidence path %q", label, ref.Path)
			continue
		}
		requireSecurityText(t, label+" symbol", ref.Symbol)
		info, err := os.Lstat(ref.Path)
		if err != nil {
			t.Errorf("%s evidence path %q: %v", label, ref.Path, err)
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			t.Errorf("%s evidence path %q is not a regular non-symlink file", label, ref.Path)
			continue
		}
		if testEvidence && !securityTestEvidencePath(ref.Path) {
			t.Errorf("%s path %q is not a test source", label, ref.Path)
		}
		if !testEvidence && securityTestEvidencePath(ref.Path) {
			t.Errorf("%s path %q is not production evidence", label, ref.Path)
		}
		if !securityEvidenceSymbolExists(t, ref) {
			t.Errorf("%s symbol %q does not exist in %s", label, ref.Symbol, ref.Path)
		}
	}
}

func securityTestEvidencePath(path string) bool {
	return strings.HasSuffix(path, "_test.go") || strings.Contains(filepath.ToSlash(path), "/__tests__/") || strings.HasSuffix(path, ".test.ts")
}

func securityEvidenceSymbolExists(t *testing.T, ref securityEvidenceRef) bool {
	t.Helper()
	if strings.HasSuffix(ref.Path, ".go") {
		file, err := parser.ParseFile(token.NewFileSet(), ref.Path, nil, 0)
		if err != nil {
			t.Errorf("parse evidence %s: %v", ref.Path, err)
			return false
		}
		for _, declaration := range file.Decls {
			switch value := declaration.(type) {
			case *ast.FuncDecl:
				if value.Name.Name == ref.Symbol {
					return true
				}
			case *ast.GenDecl:
				for _, spec := range value.Specs {
					switch item := spec.(type) {
					case *ast.TypeSpec:
						if item.Name.Name == ref.Symbol {
							return true
						}
					case *ast.ValueSpec:
						for _, name := range item.Names {
							if name.Name == ref.Symbol {
								return true
							}
						}
					}
				}
			}
		}
		return false
	}
	if strings.HasSuffix(ref.Path, ".ts") {
		data, err := os.ReadFile(ref.Path)
		if err != nil {
			t.Errorf("read TypeScript evidence %s: %v", ref.Path, err)
			return false
		}
		quoted := regexp.QuoteMeta(ref.Symbol)
		patterns := []string{
			`(?m)(?:^|\s)(?:export\s+)?(?:async\s+)?function\s+` + quoted + `\s*\(`,
			`(?m)(?:^|\s)(?:export\s+)?(?:const|let|var|class|interface|type)\s+` + quoted + `\b`,
			`(?m)^\s*(?:async\s+)?` + quoted + `\s*\(`,
		}
		for _, pattern := range patterns {
			if regexp.MustCompile(pattern).Match(data) {
				return true
			}
		}
	}
	return false
}

func securityGoFunctionCalls(t *testing.T, path, symbol string) map[string]bool {
	t.Helper()
	function := securityGoFunction(t, path, symbol)
	calls := make(map[string]bool)
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch callee := call.Fun.(type) {
		case *ast.Ident:
			calls[callee.Name] = true
		case *ast.SelectorExpr:
			if qualifier, ok := callee.X.(*ast.Ident); ok {
				calls[qualifier.Name+"."+callee.Sel.Name] = true
			}
			calls[callee.Sel.Name] = true
		}
		return true
	})
	return calls
}

func securityGoFunctionHasTrueField(t *testing.T, path, symbol, field string) bool {
	t.Helper()
	function := securityGoFunction(t, path, symbol)
	found := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		pair, ok := node.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		key, keyOK := pair.Key.(*ast.Ident)
		value, valueOK := pair.Value.(*ast.Ident)
		if keyOK && valueOK && key.Name == field && value.Name == "true" {
			found = true
		}
		return true
	})
	return found
}

func securityGoFunctionFieldContainsIdentifier(t *testing.T, path, symbol, field, identifier string) bool {
	t.Helper()
	function := securityGoFunction(t, path, symbol)
	found := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		pair, ok := node.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		key, ok := pair.Key.(*ast.Ident)
		if !ok || key.Name != field {
			return true
		}
		ast.Inspect(pair.Value, func(value ast.Node) bool {
			if name, ok := value.(*ast.Ident); ok && name.Name == identifier {
				found = true
			}
			return true
		})
		return true
	})
	return found
}

func securityRequireGoFunctionString(t *testing.T, path, symbol, value string) {
	t.Helper()
	function := securityGoFunction(t, path, symbol)
	found := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if ok && literal.Kind == token.STRING && strings.Contains(literal.Value, value) {
			found = true
		}
		return true
	})
	if !found {
		t.Fatalf("%s.%s no longer contains string marker %q，review the linked threat status", path, symbol, value)
	}
}

func securityGoFunction(t *testing.T, path, symbol string) *ast.FuncDecl {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	for _, declaration := range file.Decls {
		if function, ok := declaration.(*ast.FuncDecl); ok && function.Name.Name == symbol {
			return function
		}
	}
	t.Fatalf("function %s was not found in %s", symbol, path)
	return nil
}

func securityHTMLElementByID(t *testing.T, path, element, id string) *html.Node {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer file.Close()
	document, err := html.Parse(file)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var found *html.Node
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if found != nil {
			return
		}
		if node.Type == html.ElementNode && node.Data == element {
			if value, exists := securityHTMLAttribute(node, "id"); exists && value == id {
				found = node
				return
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(document)
	if found == nil {
		t.Fatalf("%s#%s was not found in %s", element, id, path)
	}
	return found
}

func securityHTMLAttribute(node *html.Node, name string) (string, bool) {
	for _, attribute := range node.Attr {
		if attribute.Key == name {
			return attribute.Val, true
		}
	}
	return "", false
}

func securityRequireSourcePattern(t *testing.T, path, pattern string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !regexp.MustCompile(pattern).Match(data) {
		t.Fatalf("semantic blocker marker %q is no longer present in %s，review the linked threat status", pattern, path)
	}
}

func requireBlockedSecurityThreat(t *testing.T, model securityThreatModel, id string) securityThreat {
	t.Helper()
	for _, threat := range model.Threats {
		if threat.ID != id {
			continue
		}
		if threat.Status != "blocked" {
			t.Fatalf("known blocker %s has status %q，want blocked", id, threat.Status)
		}
		requireConcreteSecurityClosure(t, "threat "+id, threat.ClosureTask)
		return threat
	}
	t.Fatalf("known blocker threat %s is missing", id)
	return securityThreat{}
}

func requireSecurityCall(t *testing.T, calls map[string]bool, call string) {
	t.Helper()
	if !calls[call] {
		t.Fatalf("expected semantic call marker %s is missing，review the linked threat status", call)
	}
}

func rejectSecurityCall(t *testing.T, calls map[string]bool, call string) {
	t.Helper()
	if calls[call] {
		t.Fatalf("security call %s is now present，review and update the linked blocker before changing this gate", call)
	}
}

func validateSecurityReferences(t *testing.T, label string, values []string, available map[string]bool) {
	t.Helper()
	if len(values) == 0 {
		t.Errorf("%s list is empty", label)
		return
	}
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if seen[value] {
			t.Errorf("duplicate %s %q", label, value)
		}
		seen[value] = true
		requireSecurityReference(t, label, value, available)
	}
}

func requireSecurityReference(t *testing.T, label, value string, available map[string]bool) {
	t.Helper()
	if !available[value] {
		t.Errorf("unknown %s %q", label, value)
	}
}

func markSecurityReferences(target map[string]bool, values []string) {
	for _, value := range values {
		target[value] = true
	}
}

func requireSecurityText(t *testing.T, label, value string) {
	t.Helper()
	if strings.TrimSpace(value) == "" {
		t.Errorf("%s is empty", label)
	}
}

func requireConcreteSecurityClosure(t *testing.T, label, value string) {
	t.Helper()
	normalized := strings.TrimSpace(value)
	if len([]rune(normalized)) < 30 || strings.EqualFold(normalized, "none") || strings.Contains(strings.ToLower(normalized), "tbd") {
		t.Errorf("%s requires a concrete closureTask，got %q", label, value)
	}
}

func securityAllowedValue(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
