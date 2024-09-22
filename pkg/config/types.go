package config

func Default() *Config {
	return &Config{
		Secret: new(Secret),
	}
}

type Config struct {
	Secret     *Secret
	Prometheus Prometheus `json:"prometheus"`
	RabbitMq   RabbitMq   `json:"rabbitmq"`
}

type Prometheus struct {
	Host string `json:"host"`
	Port string `json:"port"`
}

type RabbitMq struct {
	Host string `json:"host"`
	Port string `json:"port"`
}

type Secret struct {
	RabbitMqCredentials RabbitMqCredentials `json:"rabbitMqCredentials"`
}

type RabbitMqCredentials struct {
	User     string `json:"user"`
	Password string `json:"password"`
}
