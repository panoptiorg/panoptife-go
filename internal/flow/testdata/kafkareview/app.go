// Package kafkareview ports the review's Kafka probes (coverage wave 1
// review items 2, 4, 10 and 13). Each consumer runs three statements in
// order — Exec(payload), Exec(topic), Exec(key) — so a test can tell which
// one the topic cell reaches.
package kafkareview

import (
	"context"
	"database/sql"
	"os"

	"github.com/IBM/sarama"
	"github.com/segmentio/kafka-go"
	"github.com/twmb/franz-go/pkg/kgo"
)

// item 2: the writer names the topic; the message comes from the caller
func Publish(ctx context.Context, msg kafka.Message) error {
	w := &kafka.Writer{Topic: "orders"}
	return w.WriteMessages(ctx, msg)
}

// item 2: kafka-go rejects both at runtime; the writer is decisive
func Both(ctx context.Context, b []byte) {
	w := &kafka.Writer{Topic: "wtopic"}
	w.WriteMessages(ctx, kafka.Message{Topic: "mtopic", Value: b})
}

// item 4: only the payload goes into the cell
func Write(ctx context.Context, key, val []byte) {
	w := &kafka.Writer{Topic: "orders"}
	w.WriteMessages(ctx, kafka.Message{Key: key, Value: val})
}

// item 4: only the payload comes out of it
func Read(ctx context.Context, db *sql.DB) {
	r := kafka.NewReader(kafka.ReaderConfig{Topic: "orders"})
	m, _ := r.ReadMessage(ctx)
	db.Exec(string(m.Value))
	db.Exec(m.Topic)
	db.Exec(string(m.Key))
}

type handler struct{ db *sql.DB }

func (handler) Setup(sarama.ConsumerGroupSession) error   { return nil }
func (handler) Cleanup(sarama.ConsumerGroupSession) error { return nil }

func (h handler) ConsumeClaim(_ sarama.ConsumerGroupSession, c sarama.ConsumerGroupClaim) error {
	for m := range c.Messages() {
		h.db.Exec(string(m.Value))
		h.db.Exec(m.Topic)
		h.db.Exec(string(m.Key))
	}
	return nil
}

func Run(ctx context.Context, g sarama.ConsumerGroup, db *sql.DB) error {
	return g.Consume(ctx, []string{"orders"}, handler{db})
}

// item 13: franz-go hands each record to a callback
func Each(ctx context.Context, db *sql.DB) {
	cl, _ := kgo.NewClient(kgo.ConsumeTopics("orders"))
	cl.PollFetches(ctx).EachRecord(func(r *kgo.Record) {
		db.Exec(string(r.Value))
		db.Exec(r.Topic)
		db.Exec(string(r.Key))
	})
}

// item 10: an environment symbol and a literal that looks like one
func Env(ctx context.Context, b []byte) {
	w := &kafka.Writer{Topic: os.Getenv("PFX") + "-orders"}
	w.WriteMessages(ctx, kafka.Message{Value: b})
}

func Lit(ctx context.Context, b []byte) {
	w := &kafka.Writer{Topic: "env:PFX-orders"}
	w.WriteMessages(ctx, kafka.Message{Value: b})
}
