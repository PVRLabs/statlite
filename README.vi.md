<h1>
  <img src="internal/dashboard/static/statlite-icon.png" alt="" width="40" height="40" align="absmiddle">
  StatLite
</h1>

**StatLite** là công cụ giám sát Spring Boot nhẹ, tự lưu trữ, dành cho VPS nhỏ
và máy chủ có tài nguyên hạn chế. Chỉ cần một tệp thực thi Go chạy cùng ứng dụng,
không cần máy chủ giám sát riêng hay bộ Prometheus/Grafana. StatLite được thiết kế
để dùng ít RAM và CPU, dành tài nguyên cho ứng dụng.

Một instance StatLite tự động thu thập chỉ số và liên tục giám sát nhiều ứng dụng
đã cấu hình. Chọn ứng dụng trên dashboard chỉ thay đổi dữ liệu đang hiển thị.
Lịch sử chỉ số được lưu cục bộ trong SQLite và xem qua dashboard tích hợp sẵn;
dữ liệu ở lại trên máy chủ của bạn.

🌐 [Trang web](https://pvrlabs.xyz/statlite) · 👀 [Demo tương tác](https://pvrlabs.xyz/statlite/demo.html) · [English README](README.md)

<p align="center">
  <img src="docs/images/dashboard.webp" alt="Dashboard StatLite giám sát API thanh toán Spring Boot">
  <br><sub>Dashboard chính của StatLite khi giám sát ứng dụng Spring Boot.</sub>
</p>

## Bắt đầu nhanh

```bash
docker run --rm \
  -p 127.0.0.1:9090:9090 \
  ghcr.io/pvrlabs/statlite:latest
```

Mở <http://127.0.0.1:9090>. StatLite mặc định tự giám sát chính nó, nên dashboard
có dữ liệu trực tiếp ngay khi khởi động.

## Tài liệu đầy đủ

Xem chi tiết trong các tài liệu tiếng Anh sau:

- [Installation](docs/install.md)
- [Configuration](docs/configuration.md)
- [Supported integrations](docs/integrations.md)
- [Docker](docs/docker.md)
- [Examples](examples/)

> [README tiếng Anh](README.md) và tài liệu tiếng Anh là nguồn thông tin chính thức
> và được cập nhật mới nhất.

---

[Improve this translation](https://github.com/PVRLabs/statlite/edit/main/README.vi.md)
