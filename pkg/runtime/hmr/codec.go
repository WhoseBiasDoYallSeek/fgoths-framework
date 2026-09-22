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

import flatbuffers "github.com/google/flatbuffers/go"

// Event wire schema (hand-built against the FlatBuffers table layout, no
// flatc/.fbs compilation step involved):
//
//	table Event {
//	  kind:         string;  // field 0 -> vtable offset 4
//	  target:       string;  // field 1 -> vtable offset 6
//	  payload:      string;  // field 2 -> vtable offset 8
//	  source:       string;  // field 3 -> vtable offset 10
//	  route:        string;  // field 4 -> vtable offset 12
//	  schema_major: ushort;  // field 5 -> vtable offset 14
//	  schema_minor: ushort;  // field 6 -> vtable offset 16
//	  build_id:     string;  // field 7 -> vtable offset 18
//	  ts:           ulong;   // field 8 -> vtable offset 20 (unix nanos)
//	}
//
// Fields 5-8 are additive (schema v1.1): older buffers simply omit them and
// decode as 0/"", so an old server talking to a new client (or vice versa)
// degrades gracefully instead of breaking the wire format.
//
// Encoding/decoding uses only the flatbuffers/go runtime primitives
// (Builder + Table), the same primitives flatc-generated accessors use
// internally. This keeps the wire format real, standard FlatBuffers bytes
// while avoiding a C++ toolchain dependency in the build pipeline.
const (
	fbFieldKind        = 4
	fbFieldTarget      = 6
	fbFieldPayload     = 8
	fbFieldSource      = 10
	fbFieldRoute       = 12
	fbFieldSchemaMajor = 14
	fbFieldSchemaMinor = 16
	fbFieldBuildID     = 18
	fbFieldTS          = 20
)

// encodeEvent serializes ev as a FlatBuffers table.
func encodeEvent(ev Event) []byte {
	b := flatbuffers.NewBuilder(64 + len(ev.Payload))

	kind := b.CreateString(ev.Kind)
	target := b.CreateString(ev.Target)
	payload := b.CreateString(ev.Payload)
	source := b.CreateString(ev.Source)
	route := b.CreateString(ev.Route)
	buildID := b.CreateString(ev.BuildID)

	b.StartObject(9)
	b.PrependUOffsetTSlot(0, kind, 0)
	b.PrependUOffsetTSlot(1, target, 0)
	b.PrependUOffsetTSlot(2, payload, 0)
	b.PrependUOffsetTSlot(3, source, 0)
	b.PrependUOffsetTSlot(4, route, 0)
	b.PrependUint16Slot(5, ev.SchemaMajor, 0)
	b.PrependUint16Slot(6, ev.SchemaMinor, 0)
	b.PrependUOffsetTSlot(7, buildID, 0)
	b.PrependUint64Slot(8, ev.TS, 0)
	root := b.EndObject()

	b.Finish(root)
	return b.FinishedBytes()
}

// decodeEvent reads an Event directly out of buf without copying the
// underlying bytes for the string contents (each field is sliced from buf).
func decodeEvent(buf []byte) Event {
	if len(buf) < 4 {
		return Event{}
	}
	tbl := flatbuffers.Table{Bytes: buf, Pos: flatbuffers.GetUOffsetT(buf)}
	return Event{
		Kind:        tableString(&tbl, fbFieldKind),
		Target:      tableString(&tbl, fbFieldTarget),
		Payload:     tableString(&tbl, fbFieldPayload),
		Source:      tableString(&tbl, fbFieldSource),
		Route:       tableString(&tbl, fbFieldRoute),
		BuildID:     tableString(&tbl, fbFieldBuildID),
		SchemaMajor: tableUshort(&tbl, fbFieldSchemaMajor),
		SchemaMinor: tableUshort(&tbl, fbFieldSchemaMinor),
		TS:          tableUlong(&tbl, fbFieldTS),
	}
}

func tableString(t *flatbuffers.Table, vtableOffset flatbuffers.VOffsetT) string {
	o := t.Offset(vtableOffset)
	if o == 0 {
		return ""
	}
	return t.String(flatbuffers.UOffsetT(o) + t.Pos)
}

func tableUshort(t *flatbuffers.Table, vtableOffset flatbuffers.VOffsetT) uint16 {
	o := t.Offset(vtableOffset)
	if o == 0 {
		return 0
	}
	return t.GetUint16(flatbuffers.UOffsetT(o) + t.Pos)
}

func tableUlong(t *flatbuffers.Table, vtableOffset flatbuffers.VOffsetT) uint64 {
	o := t.Offset(vtableOffset)
	if o == 0 {
		return 0
	}
	return t.GetUint64(flatbuffers.UOffsetT(o) + t.Pos)
}
