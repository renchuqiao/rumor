package reqresp

import (
	"context"
	"encoding/binary"
	"fmt"
	"github.com/libp2p/go-libp2p-core/network"
	"github.com/libp2p/go-libp2p-core/peer"
	"io"
)

const requestBufferSize = 2048

// RequestPayloadHandler processes a request (decompressed if previously compressed), read from r.
// The handler can respond by writing to w. After returning the writer will automatically be closed.
// If the input is already known to be invalid, e.g. the request size is invalid, then `invalidInputErr != nil`, and r will not read anything more.
type RequestPayloadHandler func(ctx context.Context, peerId peer.ID, requestLen uint64, r io.Reader, w io.Writer, invalidInputErr error)

type StreamCtxFn func() context.Context

// startReqRPC registers a request handler for the given protocol. Compression is optional and may be nil.
func (handle RequestPayloadHandler) MakeStreamHandler(newCtx StreamCtxFn, comp Compression, maxRequestContentSize uint64) network.StreamHandler {
	return func(stream network.Stream) {
		peerId := stream.Conn().RemotePeer()
		ctx, cancel := context.WithCancel(newCtx())
		defer cancel()

		go func() {
			<-ctx.Done()
			_ = stream.Close() // Close stream after ctx closes.
		}()

		var invalidInputErr error

		// TODO: pool this
		blr := NewBufLimitReader(stream, requestBufferSize, 0)
		blr.N = 10 // var ints should be small
		reqLen, err := binary.ReadUvarint(blr)
		if err != nil {
			invalidInputErr = err
		} else if reqLen > maxRequestContentSize {
			// Check against raw content size limit (without compression applied)
			invalidInputErr = fmt.Errorf("request length %d exceeds request size limit %d", reqLen, maxRequestContentSize)
		} else if comp != nil {
			// Now apply compression adjustment for size limit, and use that as the limit for the buffered-limited-reader.
			s, err := comp.MaxEncodedLen(maxRequestContentSize)
			if err != nil {
				invalidInputErr = err
			} else {
				maxRequestContentSize = s
			}
		}
		// If the input is invalid, never read it.
		if invalidInputErr != nil {
			maxRequestContentSize = 0
		}
		blr.N = int(maxRequestContentSize)
		r := io.Reader(blr)
		w := io.WriteCloser(stream)
		if comp != nil {
			r = comp.Decompress(r)
			w = comp.Compress(w)
			defer w.Close()
		}
		handle(ctx, peerId, reqLen, r, w, invalidInputErr)
	}
}
