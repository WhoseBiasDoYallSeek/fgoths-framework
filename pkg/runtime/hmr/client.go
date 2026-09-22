// Copyright (c) 2026, srars-tech
// SPDX-License-Identifier: Apache-2.0
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package hmr

import (
	"fmt"
	"net/http"
	"strings"
)

// ScriptMajor/ScriptMinor mirror SchemaMajor/SchemaMinor inside the served
// JS. The constants are injected from the Go schema via fmt.Sprintf in
// ScriptHandler, so the client can never drift from the server constants.
const clientScriptTemplate = `(function () {
  "use strict";
  if (window.__fgoths_hmr) return;
  window.__fgoths_hmr = true;

  var SCHEMA_MAJOR = %d;
  var SCHEMA_MINOR = %d;

  function log(msg) {
    console.log("%%c[fgoths hmr] " + msg, "color:#00add8");
  }

  function connect() {
    var es = new EventSource("/hmr/events");
    es.addEventListener("fb", function (e) {
      var ev = decodeEvent(base64ToBytes(e.data));
      dispatch(ev);
    });
    es.onerror = function () {
      es.close();
      setTimeout(connect, 250); // server restarting — reconnect fast
      log("reconnecting…");
    };
  }

  function dispatch(ev) {
    // Compatibility gate: a foreign-major schema (or a newer minor with
    // semantics this client does not know) must never be interpreted.
    // Fallback is a safe full reload — the stale view is worse than a
    // refresh.
    if (ev.schemaMajor !== SCHEMA_MAJOR || ev.schemaMinor > SCHEMA_MINOR) {
      log("incompatible schema v" + ev.schemaMajor + "." + ev.schemaMinor +
          " (client v" + SCHEMA_MAJOR + "." + SCHEMA_MINOR + ") — reloading");
      window.location.reload();
      return;
    }
    switch (ev.kind) {
      case "reload":
        log("reloading: " + (ev.source || "change") +
            (ev.buildID ? " [build " + ev.buildID + "]" : ""));
        window.location.reload();
        return;
      case "fragment":
        if (!ev.target || (ev.route && ev.route !== window.location.pathname)) {
          return; // this tab isn't showing the page that changed
        }
        var el = document.querySelector(ev.target);
        if (!el) { log("fragment target not found: " + ev.target); return; }
        el.innerHTML = ev.payload;
        log("fragment swapped: " + ev.target + " (" + (ev.source || "change") + ")");
        return;
      case "ping":
        log("connected");
        return;
    }
  }

  // --- base64 decode (browser built-in atob, no dependency) ---
  function base64ToBytes(b64) {
    var bin = atob(b64);
    var bytes = new Uint8Array(bin.length);
    for (var i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
    return bytes;
  }

  // --- minimal FlatBuffers table reader ---
  // Mirrors the Go encoder in pkg/runtime/hmr/codec.go: a 9-field table
  // (kind, target, payload, source, route at vtable offsets 4/6/8/10/12;
  // schemaMajor, schemaMinor ushorts at 14/16; buildId string at 18; ts
  // ulong at 20). Hand-written against the FlatBuffers wire spec so the
  // client has zero dependency on the flatbuffers.js runtime.
  function decodeEvent(bytes) {
    var dv = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
    var tablePos = dv.getUint32(0, true);
    return {
      kind: fbString(dv, tablePos, 4),
      target: fbString(dv, tablePos, 6),
      payload: fbString(dv, tablePos, 8),
      source: fbString(dv, tablePos, 10),
      route: fbString(dv, tablePos, 12),
      schemaMajor: fbUshort(dv, tablePos, 14),
      schemaMinor: fbUshort(dv, tablePos, 16),
      buildID: fbString(dv, tablePos, 18),
      ts: fbUlong(dv, tablePos, 20),
    };
  }

  function fbFieldOffset(dv, tablePos, vtableOffset) {
    var soffset = dv.getInt32(tablePos, true);
    var vtablePos = tablePos - soffset;
    var vtableSize = dv.getUint16(vtablePos, true);
    if (vtableOffset >= vtableSize) return 0;
    return dv.getUint16(vtablePos + vtableOffset, true);
  }

  function fbUshort(dv, tablePos, vtableOffset) {
    var rel = fbFieldOffset(dv, tablePos, vtableOffset);
    if (!rel) return 0;
    return dv.getUint16(tablePos + rel, true);
  }

  function fbUlong(dv, tablePos, vtableOffset) {
    var rel = fbFieldOffset(dv, tablePos, vtableOffset);
    if (!rel) return 0;
    // JS numbers lose precision on 64-bit nanos; a bigint read keeps the
    // value intact but it is only used for ordering/diagnostics, so the
    // hi/lo split into Number is enough here.
    var lo = dv.getUint32(tablePos + rel, true);
    var hi = dv.getUint32(tablePos + rel + 4, true);
    return hi * 4294967296 + lo;
  }

  function fbString(dv, tablePos, vtableOffset) {
    var rel = fbFieldOffset(dv, tablePos, vtableOffset);
    if (!rel) return "";
    var fieldPos = tablePos + rel;
    var strOff = dv.getUint32(fieldPos, true);
    var strPos = fieldPos + strOff;
    var len = dv.getUint32(strPos, true);
    var bytes = new Uint8Array(dv.buffer, dv.byteOffset + strPos + 4, len);
    return utf8Decode(bytes);
  }

  function utf8Decode(bytes) {
    if (typeof TextDecoder !== "undefined") return new TextDecoder("utf-8").decode(bytes);
    var s = "";
    for (var i = 0; i < bytes.length; i++) s += String.fromCharCode(bytes[i]);
    return decodeURIComponent(escape(s));
  }

  connect();
})();
`

// ClientScript is the browser runtime served at /hmr/hmr.js, with the Go
// schema constants baked in so both sides of the wire share one source of
// truth.
var ClientScript = fmt.Sprintf(clientScriptTemplate, SchemaMajor, SchemaMinor)

// InjectClientScript inserts the HMR client script before the closing
// body tag. If no </body> exists the script is appended unchanged-safe.
func InjectClientScript(html string) string {
	tag := `<script src="/hmr/hmr.js" defer></script>`
	idx := strings.LastIndex(strings.ToLower(html), "</body>")
	if idx == -1 {
		return html + tag
	}
	return html[:idx] + tag + html[idx:]
}

// ScriptHandler serves the client script.
func ScriptHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write([]byte(ClientScript))
	}
}
