package controllers

import (
	"context"
	"fmt"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/pion/webrtc/v4"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"strings"
	"testing"
	"time"
)

var lkJoinToken, lkUrl string

func testNatsJoin(t *testing.T, token string, natsSubjects *plugnmeet.NatsSubjects) {
	ok := t.Run("Test_Nats_Join", func(t *testing.T) {
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
				defer msg.Ack()
				res := new(plugnmeet.NatsMsgServerToClient)
				err := proto.Unmarshal(msg.Data(), res)
				if !assert.NoError(t, err) {
					close(done)
					return
				}
				switch res.Event {
				case plugnmeet.NatsMsgServerToClientEvents_RES_INITIAL_DATA:
					data := new(plugnmeet.NatsInitialData)
					err := protojson.Unmarshal([]byte(res.Msg), data)
					if !assert.NoError(t, err) {
						close(done)
						return
					}
					assert.NotEmpty(t, data.MediaServerInfo.Token)
					lkJoinToken = data.MediaServerInfo.Token
					lkUrl = data.MediaServerInfo.Url
					close(done)
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

	if !ok {
		return
	}

	t.Run("Test_LK_Join", func(t *testing.T) {
		c := make(chan string)

		echoTrack, err := lksdk.NewLocalTrack(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus})
		if !assert.NoError(t, err) {
			// not possible to continue
			return
		}

		room := lksdk.NewRoom(&lksdk.RoomCallback{
			OnParticipantConnected: func(p *lksdk.RemoteParticipant) {
				t.Logf("participant connected: %v", p)
				c <- "joined"
			},
			OnDisconnected: func() {
				t.Log("Room disconnected")
				c <- "disconnected"
			},
			ParticipantCallback: lksdk.ParticipantCallback{
				OnTrackSubscribed: func(track *webrtc.TrackRemote, publication *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
					t.Log("Room received track event")
					c <- "trackSubscribed"
				},
			},
		})

		err = room.JoinWithToken(lkUrl, lkJoinToken)
		if !assert.NoError(t, err) {
			// not possible to continue
			return
		}

		_, err = room.LocalParticipant.PublishTrack(echoTrack, &lksdk.TrackPublicationOptions{
			Name: "echo",
		})
		if !assert.NoError(t, err) {
			// not possible to continue
			return
		}

		select {
		case msg := <-c:
			t.Log("Received:", msg)
			if msg == "joined" || msg == "trackSubscribed" {
				room.Disconnect()
			} else {
				return
			}
		case <-time.After(3 * time.Second):
			t.Log("Timeout: no message received.")
		}
	})
}
