// Which backend serves this page. The Go server answers /servermode.js itself with `TM_API = true`;
// everywhere else (static server, Apps Script bundle) this file keeps it false.
window.TM_API = window.TM_API || false;
