package rabbitmq

import (
	"encoding/json"
	"fmt"

	"github.com/omergorenn/sre-k8s-health-monitor/pkg/config"
	"github.com/streadway/amqp"
)

type RabbitClient struct {
	Conn *amqp.Connection
	Ch   *amqp.Channel
}

func New(appConfig *config.Config) (*RabbitClient, error) {
	connStr := fmt.Sprintf("amqp://%s:%s@%s:%s/",
		appConfig.Secret.RabbitMqCredentials.User,
		appConfig.Secret.RabbitMqCredentials.Password,
		appConfig.RabbitMq.Host,
		appConfig.RabbitMq.Port,
	)

	conn, err := amqp.Dial(connStr)
	if err != nil {
		return nil, err
	}

	return &RabbitClient{Conn: conn}, nil
}

func (r *RabbitClient) CloseConnection() {
	r.Conn.Close()
}

func (r *RabbitClient) OpenChannel() error {
	ch, err := r.Conn.Channel()
	if err != nil {
		return err
	}
	r.Ch = ch
	return nil
}

func (r *RabbitClient) CloseChannel() {
	r.Ch.Close()
}

func (r *RabbitClient) DeclareQueue(name string) error {
	_, err := r.Ch.QueueDeclare(
		name,
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return err
	}

	return nil
}

func (r *RabbitClient) PublishMessage(name string, message any) error {
	msg, err := json.Marshal(message)
	if err != nil {
		return err
	}

	err = r.Ch.Publish(
		"",
		name,
		false,
		false,
		amqp.Publishing{
			ContentType: "application/json",
			Body:        msg,
		},
	)
	if err != nil {
		return err
	}

	return nil
}
