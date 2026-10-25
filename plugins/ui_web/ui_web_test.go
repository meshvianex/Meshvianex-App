package ui_web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestRealmDetailRendersBackendExecutionGraph(t *testing.T) {
	source := uiTestSource(t)
	if strings.Contains(source, "const linkPairs=") || strings.Contains(source, "const linkPairs =") {
		t.Fatal("Realm detail must not recreate the execution topology with hard-coded link pairs")
	}
	required := []string{
		"realm.execution_graph",
		"visibleExecutionGraph",
		"layoutExecutionGraph",
		"function selectDetailComponent(realm,components,selected)",
		"selectDetailComponent(realm,components,node.dataset.executionComponent)",
		"selectDetailComponent(realm,components,button.dataset.componentSelect)",
		"Runtime services",
	}
	for _, token := range required {
		if !strings.Contains(source, token) {
			t.Fatalf("Realm detail is missing backend execution-graph renderer token %q", token)
		}
	}
}

func TestRealmGraphShowsDisabledComponentsWithReducedOpacity(t *testing.T) {
	source := uiTestSource(t)
	required := []string{
		"allNodes.filter(node=>node.kind==='component').forEach(node=>visible.add(node.id))",
		"disabled=isComponent&&!node.enabled",
		"(disabled?' is-disabled':'')",
		"(disabled?'Disabled · ':'')",
		".detail-node.execution-node.is-disabled{opacity:.38}",
		".execution-service.is-disabled{opacity:.38}",
	}
	for _, token := range required {
		if !strings.Contains(source, token) {
			t.Fatalf("Realm Graph is missing disabled-component rendering token %q", token)
		}
	}
}

func TestRealmComponentSelectionHighlightsGraphAndRuntimeServices(t *testing.T) {
	source := uiTestSource(t)
	required := []string{
		"function normalizeDetailSelection(value)",
		"function detailSelectionMatches(candidate,selected=state.detail.selected)",
		"function applyDetailSelectionHighlight(selected=state.detail.selected)",
		`data-execution-component="'+esc(selection)+'"`,
		`detailSelectionMatches(selection)?' selected':'')`,
		"applyDetailSelectionHighlight(selected)",
		".execution-service.selected{",
	}
	for _, token := range required {
		if !strings.Contains(source, token) {
			t.Fatalf("Realm component selection/highlight UI is missing token %q", token)
		}
	}
}

func TestLightThemeSelectedRealmComponentHasVisibleGlow(t *testing.T) {
	source := uiTestSource(t)
	required := []string{
		`[data-theme="light"] .detail-node.execution-node.execution-component.selected{`,
		`border-color:#236fd7`,
		`0 0 0 4px #2c83e8`,
		`0 0 24px #43c9ee99`,
	}
	for _, token := range required {
		if !strings.Contains(source, token) {
			t.Fatalf("light theme selected Realm component is missing visible highlight token %q", token)
		}
	}
}

func TestRealmTopologyExposesDedicatedDNSRoutingHandle(t *testing.T) {
	source := uiTestSource(t)
	required := []string{
		"data-route-kind=\"'+routeKind+'\"",
		"Connect Realm DNS egress",
		"DNS route updated",
		"edge.when?.purpose==='dns'",
		"configuredComponentInstances",
		"visibleExecutionGraph(realm?.execution_graph",
		"node.kind==='dns_request'",
		"Connect Realm outbound",
		"Connect Realm DNS egress",
	}
	for _, token := range required {
		if !strings.Contains(source, token) {
			t.Fatalf("DNS/component topology UI is missing token %q", token)
		}
	}
}

func TestTopologyGraphRendersLocalSource(t *testing.T) {
	source := uiTestSource(t)
	for _, value := range []string{
		"function topologyNodes(){const graphNodes=state.graph?.nodes||[]",
		"if(node.type==='source')return",
		"<h3>Local source</h3>",
	} {
		if !strings.Contains(source, value) {
			t.Fatalf("Topology Graph must render Local source: missing %q", value)
		}
	}
}

func TestCreateRealmIsRenderedAsAccessibleModal(t *testing.T) {
	source := uiTestSource(t)
	required := []string{
		`.modal-backdrop{position:fixed;inset:0;z-index:1000`,
		`.modal-backdrop.open{opacity:1;visibility:visible;pointer-events:auto}`,
		`body.modal-open{overflow:hidden}`,
		`role="dialog" aria-modal="true" aria-labelledby="create-realm-title"`,
		`modal.setAttribute('aria-hidden','false')`,
		`modal.setAttribute('aria-hidden','true')`,
		`if(event.target===event.currentTarget)closeModal()`,
		`if(event.key==='Escape')`,
		`function modalFocusable()`,
	}
	for _, token := range required {
		if !strings.Contains(source, token) {
			t.Fatalf("Create Realm modal is missing token %q", token)
		}
	}
}

func TestToastNotificationsUseFixedTimedOverlay(t *testing.T) {
	source := uiTestSource(t)
	required := []string{
		`.toast-stack{position:fixed;z-index:1200`,
		`id="toast" aria-live="polite" aria-relevant="additions" aria-label="Notifications"`,
		`.toast-item.success{--toast-accent:var(--green)}`,
		`.toast-item.error{--toast-accent:var(--red)}`,
		`.toast-progress{position:absolute;left:0;bottom:0`,
		`lifetimes={success:3600,info:4000,warning:4800,error:6000}`,
		`item.querySelector('.toast-close').addEventListener('click',dismiss)`,
		`item._toastTimer=setTimeout(dismiss,timeout)`,
		`while(stack.children.length>4)`,
	}
	for _, token := range required {
		if !strings.Contains(source, token) {
			t.Fatalf("toast notification UI is missing token %q", token)
		}
	}
	if strings.Contains(source, `window.toastTimer=setTimeout`) {
		t.Fatal("toast notifications must not use the old single-element global timer")
	}
}

func TestRealmExecutionGraphNodesAreDraggableAndPersisted(t *testing.T) {
	source := uiTestSource(t)
	required := []string{
		`meshvianex-realm-graph-positions-v1`,
		`function beginDetailNodeDrag(event)`,
		`function updateDetailNodeDrag(event)`,
		`function finishDetailNodeDrag(event)`,
		`node.setPointerCapture(event.pointerId)`,
		`document.addEventListener('pointermove',updateDetailNodeDrag)`,
		`document.addEventListener('pointercancel',finishDetailNodeDrag)`,
		`function drawDetailEdges(layout=state.detail.layout)`,
		`class="detail-graph-tools"`,
		`id="detail-reset-layout"`,
		`function resetDetailLayout()`,
		`ArrowLeft:[-1,0]`,
		`tabindex="0" aria-label="`,
		`.detail-node.execution-node.is-dragging`,
		`if(state.detail.realmId&&!state.detail.dragging)renderDetail()`,
	}
	for _, token := range required {
		if !strings.Contains(source, token) {
			t.Fatalf("draggable Realm execution graph is missing token %q", token)
		}
	}
}

func TestTopologyPointerListenersRegisterAfterTheirHandlers(t *testing.T) {
	topology, err := uiAssets.ReadFile("assets/app_topology.js")
	if err != nil {
		t.Fatal(err)
	}
	executionGraph, err := uiAssets.ReadFile("assets/app_execution_graph.js")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(topology), "updateDetailNodeDrag") || strings.Contains(string(topology), "finishDetailNodeDrag") {
		t.Fatal("topology listeners must not reference detail handlers before their script loads")
	}
	for _, token := range []string{
		"function updateDetailNodeDrag(event)",
		"function finishDetailNodeDrag(event)",
		"document.addEventListener('pointermove',updateDetailNodeDrag)",
		"document.addEventListener('pointerup',finishDetailNodeDrag)",
		"document.addEventListener('pointercancel',finishDetailNodeDrag)",
	} {
		if !strings.Contains(string(executionGraph), token) {
			t.Fatalf("execution graph listeners are missing token %q", token)
		}
	}
}

func TestNewRealmUsesStandardComponentScaffold(t *testing.T) {
	source := uiTestSource(t)
	required := []string{
		`function newRealmPayload(id,name,outboundPlugin)`,
		`routing:{instance_id:'rules-main',plugin:'io.meshvianex.rules.basic',enabled:true`,
		`dns:[{instance_id:'dns-main',plugin:'io.meshvianex.dns.system',enabled:true`,
		`protocols:[{instance_id:'protocol-main',plugin:'io.meshvianex.protocol.passthrough',enabled:true`,
		`inbounds:[{instance_id:'http-local',plugin:'io.meshvianex.inbound.http',enabled:false`,
		`default_input:'in',default_output:'out'`,
		`payload=newRealmPayload(id,name,outbound)`,
	}
	for _, token := range required {
		if !strings.Contains(source, token) {
			t.Fatalf("new Realm scaffold is missing token %q", token)
		}
	}
}

func TestRealmComponentDefaultsDoNotDuplicateConfiguredFamilies(t *testing.T) {
	source := uiTestSource(t)
	required := []string{
		`const editableDefaults=[['rules','rules-main'],['dns','dns-main'],['transform','transform-main'],['protocol','protocol-main'],['transport','transport-main'],['inbound','http-local'],['probe','probe-main']]`,
		`for(const item of configured){const key=item.type+':'+item.instance_id;if(seen.has(key))continue;seen.add(key);items.push(item)}`,
		`if(configured.some(item=>item.type===type)||items.some(item=>item.type===type))continue`,
	}
	for _, token := range required {
		if !strings.Contains(source, token) {
			t.Fatalf("Realm component default de-duplication is missing token %q", token)
		}
	}
}

func TestConfigComponentAndImportModalAreFirstClassUIFeatures(t *testing.T) {
	source := uiTestSource(t)
	required := []string{
		`id="config-import-modal"`,
		`id="config-import-input"`,
		`id="config-import-name"`,
		`id="config-import-names"`,
		`function detectedConfigName(value)`,
		`action('import_config'`,
		`componentNames.config='Config'`,
		`source_type`,
		`source_ref`,
		`round_robin`,
		`consistent_hash`,
		`sticky_session`,
		`.component-label{`,
	}
	for _, token := range required {
		if !strings.Contains(source, token) {
			t.Fatalf("Config UI is missing token %q", token)
		}
	}
	if strings.Contains(source, "async function addConfig()") || strings.Contains(source, "addConfig=async function") {
		t.Fatal("the active Add Config flow must not be implemented by a prompt-based function")
	}
	if strings.Contains(source, "&nbsp;") {
		t.Fatal("component spacing must use structural CSS instead of non-breaking-space padding")
	}
}

// uiTestSource checks the browser-delivered HTML and its locally embedded
// static assets as one logical application, without requiring inline scripts.
func uiTestSource(t *testing.T) string {
	t.Helper()
	names := regexp.MustCompile(`(?:src|href)="/assets/([^" ]+\.(?:css|js))"`).FindAllStringSubmatch(pageHTML, -1)
	if len(names) < 10 {
		t.Fatalf("expected linked JS/CSS assets, found %d", len(names))
	}
	combined := pageHTML
	for _, match := range names {
		data, err := uiAssets.ReadFile("assets/" + match[1])
		if err != nil {
			t.Fatalf("missing embedded asset %q: %v", match[1], err)
		}
		combined += "\n" + string(data)
	}
	return combined
}

func TestUIStylesAndScriptsServedAsEmbeds(t *testing.T) {
	js := regexp.MustCompile(`<script src="/assets/([^"]+\.js)"></script>`).FindAllStringSubmatch(pageHTML, -1)
	css := regexp.MustCompile(`<link rel="stylesheet" href="/assets/([^"]+\.css)">`).FindAllStringSubmatch(pageHTML, -1)
	if len(js) != 15 || len(css) != 2 {
		t.Fatalf("unexpected external assets: %d scripts, %d styles", len(js), len(css))
	}
	if strings.Contains(pageHTML, "<script>") || strings.Contains(pageHTML, "<style>") {
		t.Fatal("app should not embed its large scripts or style sheets inline")
	}
	for _, matches := range [][]string{js[0], js[len(js)-1], css[0], css[len(css)-1]} {
		target := "/assets/" + matches[1]
		req := httptest.NewRequest(http.MethodGet, target, nil)
		rec := httptest.NewRecorder()
		http.StripPrefix("/", http.FileServer(http.FS(uiAssets))).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || rec.Body.Len() == 0 {
			t.Fatalf("GET %s: status=%d size=%d", target, rec.Code, rec.Body.Len())
		}
	}
}

// Test the actual UI listener as well as the embedded static file server.
// Loading styles and scripts as classic external assets must not break the
// existing index route or the startup registration of /assets/.
func TestUIAppServesRefactoredAssets(t *testing.T) {
	instance := &Instance{cfg: Config{Listen: "127.0.0.1:0", Title: "Meshvianex test"}}
	if err := instance.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := instance.Stop(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	for _, item := range []struct {
		path     string
		contains string
	}{
		{"/", `/assets/app_bootstrap.js`},
		{"/assets/app_bootstrap.js", `const state=`},
		{"/assets/layout.css", `.app{`},
	} {
		response, err := http.Get(instance.URL() + item.path)
		if err != nil {
			t.Fatal(err)
		}
		data, readErr := io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		if readErr != nil || closeErr != nil {
			t.Fatalf("GET %s: body read=%v close=%v", item.path, readErr, closeErr)
		}
		if response.StatusCode != http.StatusOK || !strings.Contains(string(data), item.contains) {
			t.Fatalf("GET %s: status %d, missing %q", item.path, response.StatusCode, item.contains)
		}
	}
}
