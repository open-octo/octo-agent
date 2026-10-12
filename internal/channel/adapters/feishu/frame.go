package feishu

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Long-connection frames are protobuf-encoded pbbp2.Frame messages (field
// numbers from the official SDK's pbbp2.pb.go). The schema is small and
// stable, so it is hand-encoded here rather than pulling in a protobuf
// runtime for one message type.

const (
	frameMethodControl = 0 // ping / pong
	frameMethodData    = 1 // event / card

	hdrType      = "type"
	hdrMessageID = "message_id"
	hdrSum       = "sum"
	hdrSeq       = "seq"
	hdrBizRt     = "biz_rt"
)

type frameHeader struct {
	Key   string
	Value string
}

type frame struct {
	SeqID           uint64
	LogID           uint64
	Service         int32
	Method          int32
	Headers         []frameHeader
	PayloadEncoding string
	PayloadType     string
	Payload         []byte
	LogIDNew        string
}

func (f *frame) header(key string) string {
	for _, h := range f.Headers {
		if h.Key == key {
			return h.Value
		}
	}
	return ""
}

const (
	wireVarint  = 0
	wireFixed64 = 1
	wireBytes   = 2
	wireFixed32 = 5
)

func appendTag(b []byte, field int, wire int) []byte {
	return binary.AppendUvarint(b, uint64(field)<<3|uint64(wire))
}

func appendBytesField(b []byte, field int, v []byte) []byte {
	b = appendTag(b, field, wireBytes)
	b = binary.AppendUvarint(b, uint64(len(v)))
	return append(b, v...)
}

func (f *frame) marshal() []byte {
	var b []byte
	// Fields 1–4 are proto2 `required`, so they are written even when zero.
	// Field order matches the generated code, so output is byte-identical.
	b = appendTag(b, 1, wireVarint)
	b = binary.AppendUvarint(b, f.SeqID)
	b = appendTag(b, 2, wireVarint)
	b = binary.AppendUvarint(b, f.LogID)
	b = appendTag(b, 3, wireVarint)
	b = binary.AppendUvarint(b, uint64(int64(f.Service)))
	b = appendTag(b, 4, wireVarint)
	b = binary.AppendUvarint(b, uint64(int64(f.Method)))
	for _, h := range f.Headers {
		var hb []byte
		hb = appendBytesField(hb, 1, []byte(h.Key))
		hb = appendBytesField(hb, 2, []byte(h.Value))
		b = appendBytesField(b, 5, hb)
	}
	// The SDK's generated code writes the optional string fields even when
	// empty and skips only a nil payload; mirror it byte for byte.
	b = appendBytesField(b, 6, []byte(f.PayloadEncoding))
	b = appendBytesField(b, 7, []byte(f.PayloadType))
	if f.Payload != nil {
		b = appendBytesField(b, 8, f.Payload)
	}
	b = appendBytesField(b, 9, []byte(f.LogIDNew))
	return b
}

var errTruncated = errors.New("truncated protobuf")

// walkFields calls fn for each field in b. For wireVarint fields v holds the
// value; for wireBytes fields data holds the bytes. Other wire types are
// skipped.
func walkFields(b []byte, fn func(field int, v uint64, data []byte) error) error {
	for len(b) > 0 {
		tag, n := binary.Uvarint(b)
		if n <= 0 {
			return errTruncated
		}
		b = b[n:]
		field, wire := int(tag>>3), int(tag&7)
		switch wire {
		case wireVarint:
			v, n := binary.Uvarint(b)
			if n <= 0 {
				return errTruncated
			}
			b = b[n:]
			if err := fn(field, v, nil); err != nil {
				return err
			}
		case wireBytes:
			l, n := binary.Uvarint(b)
			if n <= 0 || l > uint64(len(b)-n) {
				return errTruncated
			}
			data := b[n : n+int(l)]
			b = b[n+int(l):]
			if err := fn(field, 0, data); err != nil {
				return err
			}
		case wireFixed64:
			if len(b) < 8 {
				return errTruncated
			}
			b = b[8:]
		case wireFixed32:
			if len(b) < 4 {
				return errTruncated
			}
			b = b[4:]
		default:
			return fmt.Errorf("unsupported protobuf wire type %d", wire)
		}
	}
	return nil
}

func unmarshalFrame(b []byte) (*frame, error) {
	f := &frame{}
	err := walkFields(b, func(field int, v uint64, data []byte) error {
		switch field {
		case 1:
			f.SeqID = v
		case 2:
			f.LogID = v
		case 3:
			f.Service = int32(v)
		case 4:
			f.Method = int32(v)
		case 5:
			var h frameHeader
			if err := walkFields(data, func(field int, _ uint64, data []byte) error {
				switch field {
				case 1:
					h.Key = string(data)
				case 2:
					h.Value = string(data)
				}
				return nil
			}); err != nil {
				return err
			}
			f.Headers = append(f.Headers, h)
		case 6:
			f.PayloadEncoding = string(data)
		case 7:
			f.PayloadType = string(data)
		case 8:
			f.Payload = append([]byte(nil), data...)
		case 9:
			f.LogIDNew = string(data)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return f, nil
}
