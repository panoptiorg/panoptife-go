// Package consumer reads Kafka messages into SQL. It is the reading half of
// the fixtures/kafka pair: its sarama consumer group on "orders" joins the
// producer's kafka-go writer; its decoy topic "audit-log" joins nothing.
package consumer

import (
	"context"
	"database/sql"

	"github.com/IBM/sarama"
	"github.com/segmentio/kafka-go"
	"github.com/twmb/franz-go/pkg/kgo"
)

type ordersHandler struct{ db *sql.DB }

func (ordersHandler) Setup(sarama.ConsumerGroupSession) error   { return nil }
func (ordersHandler) Cleanup(sarama.ConsumerGroupSession) error { return nil }

// ConsumeClaim is a push consumer: sarama calls it with the claim; the
// message is what is received from claim.Messages().
func (h ordersHandler) ConsumeClaim(sess sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for {
		select {
		case msg, ok := <-claim.Messages():
			if !ok {
				return nil
			}
			if _, err := h.db.Exec("INSERT INTO orders(note) VALUES ('" + string(msg.Value) + "')"); err != nil {
				return err
			}
			sess.MarkMessage(msg, "")
		case <-sess.Context().Done():
			return nil
		}
	}
}

type auditHandler struct{ db *sql.DB }

func (auditHandler) Setup(sarama.ConsumerGroupSession) error   { return nil }
func (auditHandler) Cleanup(sarama.ConsumerGroupSession) error { return nil }

func (h auditHandler) ConsumeClaim(_ sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for msg := range claim.Messages() {
		if _, err := h.db.Exec("INSERT INTO audit(event) VALUES ('" + string(msg.Value) + "')"); err != nil {
			return err
		}
	}
	return nil
}

// Run binds the handlers to their topics.
func Run(ctx context.Context, group sarama.ConsumerGroup, db *sql.DB) error {
	if err := group.Consume(ctx, []string{"orders"}, ordersHandler{db: db}); err != nil {
		return err
	}
	return group.Consume(ctx, []string{"audit-log"}, &auditHandler{db: db})
}

// Poll is a pull consumer on the decoy topic with kafka-go.
func Poll(ctx context.Context, db *sql.DB) error {
	r := kafka.NewReader(kafka.ReaderConfig{Brokers: []string{"kafka:9092"}, Topic: "audit-log", GroupID: "audit"})
	defer r.Close()
	for {
		m, err := r.ReadMessage(ctx)
		if err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, "DELETE FROM audit WHERE id = '"+string(m.Key)+"'"); err != nil {
			return err
		}
	}
}

// Drain is a franz-go pull consumer on "orders": the fetches are the message.
func Drain(ctx context.Context, db *sql.DB) error {
	cl, err := kgo.NewClient(kgo.SeedBrokers("kafka:9092"), kgo.ConsumeTopics("orders"))
	if err != nil {
		return err
	}
	defer cl.Close()
	for _, rec := range cl.PollFetches(ctx).Records() {
		if _, err := db.ExecContext(ctx, "INSERT INTO orders(note) VALUES ('"+string(rec.Value)+"')"); err != nil {
			return err
		}
	}
	return nil
}
