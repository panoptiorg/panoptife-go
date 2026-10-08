// Package producer publishes request data to Kafka. It is the writing half of
// a coverage wave 1 fixture pair: the topic "orders" joins it to
// fixtures/kafka/consumer, the decoy topic "audit-events" joins nothing.
package producer

import (
	"net/http"
	"os"

	"github.com/IBM/sarama"
	"github.com/segmentio/kafka-go"
	"github.com/twmb/franz-go/pkg/kgo"
)

func Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /orders", PlaceOrder)
	mux.HandleFunc("POST /audit", Audit)
}

// PlaceOrder writes the untrusted note to topic "orders" (kafka-go Writer
// literal: the topic is on the writer).
func PlaceOrder(w http.ResponseWriter, r *http.Request) {
	note := r.FormValue("note")
	writer := &kafka.Writer{Addr: kafka.TCP("kafka:9092"), Topic: "orders"}
	defer writer.Close()
	if err := writer.WriteMessages(r.Context(), kafka.Message{Key: []byte("k"), Value: []byte(note)}); err != nil {
		http.Error(w, "kafka", http.StatusBadGateway)
	}
}

// Audit writes to the decoy topic: a different name, so a different cell.
func Audit(w http.ResponseWriter, r *http.Request) {
	writer := kafka.NewWriter(kafka.WriterConfig{Brokers: []string{"kafka:9092"}, Topic: "audit-events"})
	_ = writer.WriteMessages(r.Context(), kafka.Message{Value: []byte(r.FormValue("event"))})
}

// Relay publishes with sarama's sync producer; the topic is on the message
// and comes from the environment — the symbolic topic env:RELAY_TOPIC.
func Relay(p sarama.SyncProducer, body string) error {
	_, _, err := p.SendMessage(&sarama.ProducerMessage{
		Topic: os.Getenv("RELAY_TOPIC"),
		Value: sarama.StringEncoder(body),
	})
	return err
}

// Forward publishes with franz-go; the topic is a parameter of an in-house
// wrapper, which wave 1 does not resolve (census: from_param).
func Forward(cl *kgo.Client, r *http.Request, topic string) {
	cl.Produce(r.Context(), &kgo.Record{Topic: topic, Value: []byte(r.FormValue("x"))}, nil)
}

// Enqueue publishes with sarama's async producer: the produce is the send on
// the channel Input() returns.
func Enqueue(p sarama.AsyncProducer, r *http.Request) {
	p.Input() <- &sarama.ProducerMessage{Topic: "orders", Value: sarama.ByteEncoder(r.FormValue("note"))}
}

// Mirror publishes with franz-go to the client's default topic.
func Mirror(r *http.Request) error {
	cl, err := kgo.NewClient(kgo.SeedBrokers("kafka:9092"), kgo.DefaultProduceTopic("audit-events"))
	if err != nil {
		return err
	}
	defer cl.Close()
	return cl.ProduceSync(r.Context(), &kgo.Record{Value: []byte(r.FormValue("event"))}).FirstErr()
}
