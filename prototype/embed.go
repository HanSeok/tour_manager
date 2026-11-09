// Package prototype embeds the dashboard's front-end files so the Go server ships as one binary.
// (Test files and e2e scripts are deliberately not listed.)
package prototype

import "embed"

//go:embed index.html app.js data.js hours.js money.js remote.js route.js servermode.js slots.js schedule.js settle.js timeline.js
var Files embed.FS
