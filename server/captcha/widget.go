package captcha

import _ "embed"

// WidgetJS, GET /captcha/v1/api.js ile sunulan widget betigi (disariya kaynak yok).
//
//go:embed widget/api.js
var WidgetJS []byte
