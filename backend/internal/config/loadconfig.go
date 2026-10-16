package config

import (
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Redis    RedisConfig    `yaml:"redis"`
	RabbitMQ RabbitMQConfig `yaml:"rabbitmq"`
}

type ServerConfig struct {
	Port int `yaml:"port"`
}

type DatabaseConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	DBName   string `yaml:"dbname"`
}

type RedisConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
}

type RabbitMQConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

func Load(filename string) (Config, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return Config{}, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}

	// K8s 部署时用环境变量覆盖 YAML，环境变量统一大写 + 下划线分隔（如 DB_HOST / REDIS_PORT）
	cfg.applyEnv()
	return cfg, nil
}

func (c *Config) applyEnv() {
	if v, ok := os.LookupEnv("SERVER_PORT"); ok {
		if p, err := strconv.Atoi(v); err == nil {
			c.Server.Port = p
		}
	}

	if v, ok := os.LookupEnv("DB_HOST"); ok {
		c.Database.Host = v
	}
	if v, ok := os.LookupEnv("DB_PORT"); ok {
		if p, err := strconv.Atoi(v); err == nil {
			c.Database.Port = p
		}
	}
	if v, ok := os.LookupEnv("DB_USER"); ok {
		c.Database.User = v
	}
	if v, ok := os.LookupEnv("DB_PASSWORD"); ok {
		c.Database.Password = v
	}
	if v, ok := os.LookupEnv("DB_NAME"); ok {
		c.Database.DBName = v
	}

	if v, ok := os.LookupEnv("REDIS_HOST"); ok {
		c.Redis.Host = v
	}
	if v, ok := os.LookupEnv("REDIS_PORT"); ok {
		if p, err := strconv.Atoi(v); err == nil {
			c.Redis.Port = p
		}
	}
	if v, ok := os.LookupEnv("REDIS_PASSWORD"); ok {
		c.Redis.Password = v
	}
	if v, ok := os.LookupEnv("REDIS_DB"); ok {
		if n, err := strconv.Atoi(v); err == nil {
			c.Redis.DB = n
		}
	}

	if v, ok := os.LookupEnv("RABBITMQ_HOST"); ok {
		c.RabbitMQ.Host = v
	}
	if v, ok := os.LookupEnv("RABBITMQ_PORT"); ok {
		if p, err := strconv.Atoi(v); err == nil {
			c.RabbitMQ.Port = p
		}
	}
	if v, ok := os.LookupEnv("RABBITMQ_USER"); ok {
		c.RabbitMQ.Username = v
	}
	if v, ok := os.LookupEnv("RABBITMQ_PASSWORD"); ok {
		c.RabbitMQ.Password = v
	}
}
