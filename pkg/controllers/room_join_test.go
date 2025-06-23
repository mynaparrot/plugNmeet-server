package controllers

import (
	"bytes"
	"context"
	"fmt"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testValidateJoinToken(t *testing.T, token string) {
	t.Run("Test_Validate_Join_Token", func(t *testing.T) {
		app := setupApp()
		reqBody := &plugnmeet.VerifyTokenReq{}

		bodyBytes, err := proto.Marshal(reqBody)
		assert.NoError(t, err)
		req := httptest.NewRequest("POST", "/api/verifyToken", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/octet-stream")
		req.Header.Set("Authorization", token)

		// Send request
		resp, err := app.Test(req)
		assert.NoError(t, err)
		assert.Equal(t, 200, resp.StatusCode)

		buf := new(bytes.Buffer)
		_, err = buf.ReadFrom(resp.Body)
		assert.NoError(t, err)

		// Read and unmarshal response
		respBody := new(plugnmeet.VerifyTokenRes)
		err = proto.Unmarshal(buf.Bytes(), respBody)
		assert.NoError(t, err)

		// Compare expected values
		assert.True(t, respBody.Status)
		assert.Equal(t, "token is valid", respBody.Msg)
		assert.NotNil(t, respBody.NatsSubjects)

		// now we can run some other test
		nts := NewNatsController()
		go nts.BootUp()

		// wait until finish bootup
		time.Sleep(time.Second * 1)
		testNatsJoin(t, token, respBody.NatsSubjects)
	})
}

func testNatsJoin(t *testing.T, token string, natsSubjects *plugnmeet.NatsSubjects) {
	t.Run("Test_Nats_Join", func(t *testing.T) {
		nc, err := nats.Connect(strings.Join(config.GetConfig().NatsInfo.NatsUrls, ","), nats.Token(token))
		if !assert.NoError(t, err) {
			// not possible to continue
			return
		}

		js, err := jetstream.New(nc)
		assert.NoError(t, err)

		// now subscribe
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		stream, err := js.Stream(ctx, roomId)
		if !assert.NoError(t, err) {
			// not possible to continue
			return
		}

		cons, err := stream.Consumer(ctx, fmt.Sprintf("%s:%s", natsSubjects.SystemPrivate, userId))
		if !assert.NoError(t, err) {
			// not possible to continue
			return
		}
		done := make(chan struct{})
		go func() {
			cc, err := cons.Consume(func(msg jetstream.Msg) {
				defer func() {
					msg.Ack()
					close(done)
				}()
				res := new(plugnmeet.NatsMsgServerToClient)
				err := proto.Unmarshal(msg.Data(), res)
				if !assert.NoError(t, err) {
					return
				}
				switch res.Event {
				case plugnmeet.NatsMsgServerToClientEvents_RES_INITIAL_DATA:
					data := new(plugnmeet.NatsInitialData)
					err := protojson.Unmarshal([]byte(res.Msg), data)
					if !assert.NoError(t, err) {
						return
					}
					assert.NotEmpty(t, data.MediaServerInfo.Token)
				}
			})
			assert.NoError(t, err)
			defer cc.Stop()
			<-done
		}()

		// send a test
		subj := fmt.Sprintf("%s.%s.%s", natsSubjects.SystemJsWorker, roomId, userId)
		payload, err := proto.Marshal(&plugnmeet.NatsMsgClientToServer{
			Event: plugnmeet.NatsMsgClientToServerEvents_REQ_INITIAL_DATA,
		})
		if !assert.NoError(t, err) {
			// not possible to continue
			return
		}

		_, err = js.Publish(ctx, subj, payload)
		if !assert.NoError(t, err) {
			// not possible to continue
			return
		}

		select {
		case <-done:
			// proceed with cleanup
		case <-time.After(3 * time.Second): // timeout fallback
			close(done)
		}
	})
}
