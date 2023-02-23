package boot

import "github.com/hotafrika/tcp_proxy_epoll/service"

type Config struct {
	Apps []App `json:"Apps"`
}

type App struct {
	Name    string   `json:"Name"`
	Ports   []int    `json:"Ports"`
	Targets []string `json:"Targets"`
}

func (c Config) toProxyConfig() service.ProxyConfig {
	var proxyConfig service.ProxyConfig
	for _, app := range c.Apps {
		configApp := service.ConfigApp{
			Name:    app.Name,
			Ports:   app.Ports,
			Targets: app.Targets,
		}
		proxyConfig.Apps = append(proxyConfig.Apps, configApp)
	}
	return proxyConfig
}
