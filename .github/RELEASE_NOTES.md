GửiWeb v0.1.2 bổ sung link đăng ký dùng chung cho nhiều người.

- Admin tạo và thu hồi link; người nhận có thể chuyển tiếp cho bạn bè tự
  đăng ký tài khoản thành viên, không được cấp quyền admin.
- Giao diện mặc định 20 lượt, cho phép số nguyên 1–1000. Link hơn một lượt
  hết hạn sau 7 ngày; link một lượt hết hạn sau 48 giờ.
- Hiển thị số đăng ký thành công, lượt còn lại, thời hạn và trạng thái.
  Hết lượt, hết hạn hoặc thu hồi đều chặn đăng ký mới; thu hồi không xóa
  các tài khoản đã đăng ký.
- Sửa nút tải lên không phản hồi khi HTML mới được ghép với JavaScript cũ
  trong cache trình duyệt. JS/CSS quản lý có URL theo hash nội dung; HTML
  dùng `no-store`, asset dùng `no-cache` để các phiên bản khớp nhau.
- Website do admin sở hữu dùng ngân sách tổng, không chịu quota cá nhân.
  Admin cập nhật website thành viên vẫn giữ chủ sở hữu và quota người đó.
  Các giới hạn upload, website, tổng dung lượng và khoảng trống vẫn áp dụng.

Nâng cấp tự bổ sung cột đếm lượt, giữ schema version 1 và dữ liệu cũ. Link
một lần đã có được giữ trạng thái. Quay lại binary v0.1.0/v0.1.1 không cần
khôi phục DB; link nhiều lượt không đăng ký được trên bản cũ. Số lượt đã
dùng được giữ lại và tiếp tục dùng khi chạy v0.1.2 nếu còn hạn/lượt và chưa
bị thu hồi.

Tải **guiweb-openwrt-arm64.tar.gz** và file **.sha256** đi kèm. Giữ
`/etc/guiweb/config.json` và `/overlay/guiweb`; không tạo lại admin, reset
mật khẩu, xóa dữ liệu hoặc thay Tunnel. Chưa xác nhận nâng cấp trên router thật.

Sau khi nâng cấp, tải lại trang quản lý một lần bằng **Ctrl+Shift+R**. Việc
đổi quota admin là thay đổi riêng; lỗi nút không phản hồi bắt nguồn từ JS cũ
không khớp HTML mới.

[Hướng dẫn nâng cấp v0.1.2](https://github.com/NguyenPhat-NT/chia_se_web/blob/v0.1.2/docs/UPGRADE_0.1.2.md).
[Hướng dẫn cài mới OpenWrt](https://github.com/NguyenPhat-NT/chia_se_web/blob/v0.1.2/docs/OPENWRT.md).
