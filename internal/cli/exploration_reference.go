package cli

// Explicitly approved command metadata is registered once at package startup,
// never while constructing a command tree (which may happen concurrently).
func init() {
	groups := map[string]string{
		"storage": "Configure host-local artifact storage preferences.",
		"input":   "Bind external inputs without copying data into Git.",
		"runner":  "Configure portable project runner commands and version identity.",
		"results": "Locate and retrieve artifacts using exact Try and Attempt ownership.",
		"history": "Search descriptions and evidence across canonical records.",
	}
	for name, summary := range groups {
		approvedCommandReference["exp "+name] = commandReferenceSpec{use: "exp " + name, summary: summary}
	}
	for path, spec := range map[string]commandReferenceSpec{
		"try root":         {use: "exp try root [PATH] [--json]", summary: "Inspect or set the adhoc collection root without moving previous explorations."},
		"try start":        {use: "exp try start --title TITLE --goal GOAL [--scratch] [--tags TAGS] [--json]", summary: "Create an open Try and optionally an isolated small Git Source for adhoc work."},
		"try exec":         {use: "exp try exec TRY [--storage PROFILE] [--runner PROFILE] [--input NAME] [--dirty=capture] [--allow GLOB] [--json] -- [COMMAND [ARG...]]", summary: "Append a new Attempt with independent managed outputs, input identities and runner provenance."},
		"try summarize":    {use: "exp try summarize TRY --summary TEXT [--author agent|human] [--json]", summary: "Save an attributed working summary without changing the Try lifecycle."},
		"storage show":     {use: "exp storage show [--global] [--json]", summary: "Inspect private storage profiles, effective preference and its origin."},
		"storage add":      {use: "exp storage add NAME --root DIR [--kind local|mlflow-local|mlflow-remote] [--tracking-uri URI] [--python BINARY] [--token-env NAME] [--large-bytes N] [--json]", summary: "Remember storage routing; MLflow metadata and artifact bytes use separate stores."},
		"storage use":      {use: "exp storage use NAME [--global] [--json]", summary: "Persist the storage default for the current Project or globally."},
		"input bind":       {use: "exp input bind NAME PATH [--global] [--json]", summary: "Remember a private external data binding and inspect its content identity."},
		"input list":       {use: "exp input list [--global] [--json]", summary: "Inspect effective host-local input bindings."},
		"runner add":       {use: "exp runner add NAME [--version-arg ARG] [--environment-file PATH] [--json] -- COMMAND [ARG...]", summary: "Remember project CLI argv and optional JSON version and lockfile probes."},
		"runner use":       {use: "exp runner use NAME [--global] [--json]", summary: "Remember the current Project or global runner preference."},
		"runner list":      {use: "exp runner list [--json]", summary: "List configured runner profiles."},
		"results describe": {use: "exp results describe ATTEMPT NAME --description TEXT [--json]", summary: "Annotate one artifact without changing its content identity."},
		"results list":     {use: "exp results list TRY|ATTEMPT [--json]", summary: "List artifact identities and provider-free local availability."},
		"results compare":  {use: "exp results compare TRY|ATTEMPT TRY|ATTEMPT... [--json]", summary: "Compare named outputs and the exact runner identities that produced them."},
		"results save":     {use: "exp results save TRY|ATTEMPT [--allow-large] [--json]", summary: "Retry pending artifact publication without re-executing the workload."},
		"results fetch":    {use: "exp results fetch TRY|ATTEMPT NAME [--destination PATH] [--json]", summary: "Retrieve one unambiguous named artifact and verify its digest."},
		"results open":     {use: "exp results open TRY|ATTEMPT NAME [--destination PATH] [--json]", summary: "Retrieve and verify one named artifact, then invoke the desktop file opener."},
		"history search":   {use: "exp history search [WORDS...] [--all] [--kind KIND] [--source-key SOURCE] [--version VERSION] [--state STATE] [--after DATE] [--before DATE] [--limit N] [--json]", summary: "Search canonical descriptions, tags, versions and artifact names using a rebuildable SQLite cache."},
	} {
		spec.flags = map[string]string{"json": jsonFlagUsage}
		approvedCommandReference["exp "+path] = spec
	}
	spec := approvedCommandReference["exp try run"]
	spec.use = "exp try run --title TITLE --goal GOAL [--outputs] [--storage PROFILE] [--runner PROFILE] [--input NAME] [--dirty=capture] [--allow GLOB] [--json] -- COMMAND [ARG...]"
	spec.summary = "Run a bounded Try; opt into managed artifact outputs or use try start/exec for continuing exploration."
	approvedCommandReference["exp try run"] = spec
	spec = approvedCommandReference["exp try finish"]
	spec.use = "exp try finish TRY --summary TEXT [--author human|agent] [--saved-results|--result-digest SHA256|--external-ref JSON|--no-results] [--confirm] [--json]"
	spec.summary = "Conclude a Try with attributed observations; agent conclusions remain explicitly unreviewed."
	approvedCommandReference["exp try finish"] = spec
}
