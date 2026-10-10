GửiWeb cho nhóm người quen chia sẻ website tĩnh trên OpenWrt/Kwrt.

- Tài khoản qua link mời một lần; thành viên quản lý website của mình, admin quản lý tất cả.
- Upload HTML, ZIP hoặc thư mục; đổi tên, xóa và cập nhật giữ nguyên link.
- Giao diện tiếng Việt, sáng/tối, hỗ trợ điện thoại.
- Go + SQLite, binary Linux ARM64 tĩnh, không cần Node.js trên router.

Tải **guiweb-openwrt-arm64.tar.gz** và file **.sha256** đi kèm. Gói chứa binary,
checksum binary, cấu hình, dịch vụ procd và đoạn route Tunnel bổ sung.

Cấu hình đã điền `app.phatnt.id.vn` và `phatnt.id.vn`, dành riêng `in4` cho
dịch vụ hiện có. Dữ liệu tại `/overlay/guiweb`; upload 50 MiB, website 100 MiB,
thành viên 128 MiB, tổng dữ liệu 512 MiB. Kiểm tra lại cấu hình trước khi cài.
Không có mật khẩu mặc định; tạo admin bằng CLI tại lời nhắc ẩn.

Kiểm chứng cloud: 24 bài test, 144 ca tính cả subtest và kiểm tra race đạt;
luồng trình duyệt, upload đúng 50 MiB và dữ liệu sau restart đạt.

Chưa kiểm chứng trên router thật hoặc public HTTPS. Cài theo
[hướng dẫn OpenWrt](https://github.com/NguyenPhat-NT/chia_se_web/blob/main/docs/OPENWRT.md).
Giữ nguyên route/DNS của `in4.phatnt.id.vn`, đặt các route cụ thể trước wildcard.
Không format hoặc thay đổi phân vùng để cài ứng dụng.
