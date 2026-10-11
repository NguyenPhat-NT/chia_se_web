GửiWeb v0.1.1 cho phép dùng lại tên website đã xóa và tự nhận dạng nội dung upload.

- Xóa website giải phóng slug trong cùng transaction. Bất kỳ thành viên nào
  cũng có thể tạo website mới bằng tên vừa xóa.
- Khi khởi động, tự dọn những slug giữ chỗ từ v0.1.0 không còn website tương
  ứng. Website đang có, tài khoản và cấu hình được giữ nguyên.
- Tên đang sử dụng vẫn duy nhất. Các tên dành riêng như `app`, `in4`, `www`,
  `api`, `admin` và các tên trong cấu hình vẫn bị chặn.
- Giao diện tự nhận dạng HTML/ZIP khi chọn file, hoặc nội dung khi chọn thư
  mục; không cần chọn loại upload thủ công. Cả tạo mới và cập nhật đều dùng
  cách chọn này.

Nếu một slug được dùng lại, link cũ hiển thị website mới. Xóa trên router
không xóa `localStorage` hoặc service worker lưu trong trình duyệt người xem.

Tải **guiweb-openwrt-arm64.tar.gz** và file **.sha256** đi kèm. Với router đã
cài v0.1.0, chỉ thay binary sau khi kiểm tra checksum; giữ
`/etc/guiweb/config.json` và `/overlay/guiweb`. Không cần tạo lại admin, reset
mật khẩu hoặc thay Tunnel. DB vẫn tương thích binary v0.1.0 nếu cần quay lại.

Chưa xác nhận nâng cấp trên router thật.

[Hướng dẫn nâng cấp v0.1.1](https://github.com/NguyenPhat-NT/chia_se_web/blob/v0.1.1/docs/UPGRADE_0.1.1.md).
[Hướng dẫn cài mới OpenWrt](https://github.com/NguyenPhat-NT/chia_se_web/blob/v0.1.1/docs/OPENWRT.md).
