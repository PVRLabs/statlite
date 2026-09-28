<h1>
  <img src="internal/dashboard/static/statlite-icon.png" alt="" width="40" height="40" align="absmiddle">
  StatLite
</h1>

**StatLite** 是一款轻量级、自托管的指标仪表盘，单个 Go 二进制文件即可运行，使用
SQLite 存储数据，专为 VPS 和小型服务器上的应用而设计。它可以监控流量、延迟、
JVM/进程资源、健康状态，以及可选的主机指标，支持 Spring Boot、Quarkus，以及
通过简单的 StatLite Metrics 接口接入的其他框架和语言。

指标数据留在你自己的服务器上。StatLite 在你的服务器上采集，并把历史记录存在
SQLite 里，不需要把应用指标持续送到第三方监控 SaaS。

🌐 [官网](https://pvrlabs.xyz/statlite) · 👀 [在线演示](https://pvrlabs.xyz/statlite/demo.html) · [English README](README.md)

<p align="center">
  <img src="docs/images/dashboard.webp" alt="StatLite dashboard monitoring a Spring Boot payments API">
  <br><sub>StatLite 监控 Spring Boot 应用的主仪表盘。</sub>
</p>

## 快速体验

```bash
docker run --rm \
  -p 127.0.0.1:9090:9090 \
  ghcr.io/pvrlabs/statlite:latest
```

打开 <http://127.0.0.1:9090>，StatLite 默认会监控自身，因此仪表盘启动后即有
实时数据。

## 支持的集成

- Spring Boot
- Quarkus
- [StatLite Metrics v1](docs/statlite-metrics-v1.md)：适用于其他框架和语言的
  简单 JSON 接口

## 完整文档

以下英文文档提供完整细节：

- [Installation](docs/install.md)
- [Configuration](docs/configuration.md)
- [Supported integrations](docs/integrations.md)
- [Docker](docs/docker.md)
- [Examples](examples/)

> 英文版 [README](README.md) 及其文档是权威且最新的信息来源。

---

[Improve this translation](https://github.com/PVRLabs/statlite/edit/main/README.zh-Hans.md)
