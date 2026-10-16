GửiWeb v0.1.3 bổ sung kho khóa API, relay Telegram, dán HTML và prompt AI.

- **Khóa API** theo từng website: chỉ chủ site được quản lý, kể cả admin
  không quản lý kho khóa của người khác. Tối đa 20 tên/site, mẫu
  `[A-Z][A-Z0-9_]{0,63}`. Giá trị mã hóa AES-GCM, không trả lại khóa rõ;
  hỗ trợ thay và thu hồi.
- **Telegram Bot** lưu bot token và chat đích cố định. Site gọi
  `POST /__guiweb/telegram/TEN_KHOA` với JSON chỉ chứa `text`; browser cần
  Origin đúng, không CORS. Relay chỉ gửi văn bản thường bằng Telegram
  `sendMessage`; không nhận token/chat/URL tùy ý từ client.
- Relay công khai với khách truy cập site, không xác thực riêng người gửi.
  Giới hạn 5 tin/phút/IP + site, 20 tin/phút/site, 60 tin/phút chung,
  4 request đồng thời toàn hệ thống, timeout 10 giây. Generic key chỉ lưu, chưa có proxy
  API đa mục đích.
- **Dán HTML** để tạo/cập nhật `index.html`, áp dụng quota hiện có. Cập nhật
  giữ link nhưng thay toàn bộ bộ file cũ.
- **Tạo với AI** tạo prompt và sao chép clipboard để tự dùng với công cụ AI;
  không tích hợp gọi AI trả phí hoặc chạy backend tùy ý.

Nâng cấp thêm bảng khóa, giữ schema version 1, config, tài khoản và website.
Master key ở `<data_dir>/.secrets-key`, quyền 600, do service tự sinh khi
chưa có secret. Luôn giữ file này cùng DB khi sao lưu/khôi phục: mất key
khi đã lưu secret khiến dữ liệu khóa không giải mã được và service từ chối
khởi động. Quay lại binary cũ không cần khôi phục DB; bản cũ không dùng
vault/key file, cần giữ file nguyên vẹn để chạy lại bản mới.

Tải **guiweb-openwrt-arm64.tar.gz** và file **.sha256**. Giữ
`/etc/guiweb/config.json`, `/overlay/guiweb` và `.secrets-key` nếu đã có;
không tạo lại admin, reset mật khẩu hoặc thay Tunnel. Sau nâng cấp tải lại
trang bằng **Ctrl+Shift+R**. Chưa xác nhận nâng cấp hoặc relay trên router thật.

[Hướng dẫn nâng cấp v0.1.3](https://github.com/NguyenPhat-NT/chia_se_web/blob/v0.1.3/docs/UPGRADE_0.1.3.md).
[Hướng dẫn OpenWrt](https://github.com/NguyenPhat-NT/chia_se_web/blob/v0.1.3/docs/OPENWRT.md).
