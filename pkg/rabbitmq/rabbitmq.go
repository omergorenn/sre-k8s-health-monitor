package rabbitmq

import (
	"encoding/json"
	"fmt"

	"github.com/omergorenn/sre-k8s-health-monitor/pkg/config"
	"github.com/streadway/amqp"
	"go.uber.org/zap"
)

type RabbitClient struct {
	Conn *amqp.Connection
	Ch   *amqp.Channel
}

func NewClient(appConfig *config.Config) (*RabbitClient, error) {
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

func (r *RabbitClient) closeConnection() {
	r.Conn.Close()
}

func (r *RabbitClient) openChannel() error {
	ch, err := r.Conn.Channel()
	if err != nil {
		return err
	}
	r.Ch = ch
	return nil
}

func (r *RabbitClient) closeChannel() {
	r.Ch.Close()
}

func (r *RabbitClient) declareQueue(name string) error {
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

func (r *RabbitClient) publishMessage(name string, message any) error {
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

func (r *RabbitClient) PublishEvent(name string, message any) error {
	err := r.openChannel()
	if err != nil {
		zap.L().Fatal("Failed to open a channel ", zap.Error(err))
		return err
	}

	defer r.closeChannel()

	if err := r.declareQueue("node_alerts"); err != nil {
		zap.L().Fatal("failed to declare queue ", zap.Error(err))
		return err
	}

	if err := r.publishMessage("node_alerts", message); err != nil {
		zap.L().Fatal("failed to publish message ", zap.Error(err))
		return err
	}

	return nil
}
