# Kết quả kiểm chứng bản đầu

## Đã chạy trong cloud

| Kiểm tra | Kết quả |
| --- | --- |
| SDK Go 1.27.2 từ go.dev, SHA-256 chính thức | Đạt |
| `scripts/install-dev.sh`, chạy lặp lại | Đạt, module/lockfile không bị sửa |
| `go test -race -mod=readonly -json ./...` | 24 test cấp đầu, 144 ca tính cả subtest đều đạt; không có test bị bỏ qua |
| `go vet ./...` | Đạt |
| `node --check internal/app/web/app.js` | Đạt |
| Shell scripts và JSON cấu hình | Hợp lệ |
| Build Linux amd64 và arm64, `CGO_ENABLED=0` | Đạt, ELF tĩnh, binary arm64 khoảng 11 MiB |
| Readiness với Host quản lý; hostname không hợp lệ | `status: ok`; Host sai trả 404 |

Package `cmd/guiweb` chưa có unit test riêng; CLI được kiểm chứng trực tiếp
bằng terminal có PTY để tạo admin rồi chạy/restart server. Package được runner
ghi `skip` vì không có test riêng, không phải một assertion được bỏ qua.

Các ca backend kiểm tra lời mời một lần/hết hạn/thu hồi, rollback khi tên tài
khoản trùng, cookie, Origin/CSRF, quyền sở hữu, reset password, rate limiting
qua Tunnel, cấu hình, khóa dữ liệu, bảo vệ file có sẵn, quota và cấu trúc ZIP.
Các ca đồng thời kiểm tra stream đang mở không chặn cập nhật/xóa. Restart
kiểm tra dọn staging/release mồ côi và giữ bản đang hoạt động.

## Trình duyệt và kích thước upload thực tế

Chromium chạy với backend thật đã vượt qua:

- Đăng nhập, upload HTML, ZIP và payload thư mục có `webkitRelativePath`.
- Mở website trên hostname riêng, tải CSS/JavaScript bằng đường dẫn tuyệt đối.
- Đổi tên, cập nhật nội dung nhưng giữ nguyên link, xóa và thu hồi public link.
- Tạo link mời, đăng ký thành viên, tách danh sách website, đăng xuất.
- Giao diện mobile 390 px, sáng/tối, không tràn ngang.
- Không có lỗi JavaScript hoặc vi phạm Content Security Policy trong luồng này.

Trong bài test thư mục, File API cung cấp đường dẫn tương đối cho input rồi
gửi multipart thật; hộp chọn thư mục native của hệ điều hành chưa được tự động
kiểm chứng. Cần thử thao tác chọn thư mục trên trình duyệt sử dụng thực tế.

Một lượt upload HTML đúng 52.428.800 byte (50 MiB) đã trả 201. Sau restart,
session và metadata còn dùng được, website trả đúng Content-Length 50 MiB.
Trong lần đo trên Linux amd64 với `GOMEMLIMIT=64MiB`, RSS lấy mẫu khoảng
12.072 KiB lúc rảnh và 12.852 KiB trong upload. Đây là số đo mẫu trong cloud,
không phải giới hạn RAM hoặc kết quả benchmark trên router/ZIP/truy cập đông.

## Chuẩn bị cho router

Đã tạo `bin/guiweb-openwrt-arm64.tar.gz` gồm binary, checksum, cấu hình mẫu,
dịch vụ procd và hướng dẫn. Checksum của binary trong gói khớp artifact build.
Chủ thiết bị đã cung cấp chẩn đoán aarch64, cloudflared cùng router, cổng 8088
chưa dùng, `/overlay` còn khoảng 1,8 GiB và RAM khả dụng khoảng 133 MiB.

Hạn mức mặc định hiện tại: upload 50 MiB, website 100 MiB, thành viên
128 MiB, tổng dữ liệu 512 MiB, production giữ trống 512 MiB. Phân vùng lớn
chưa được mount/nhận diện filesystem không được tự format để cài ứng dụng.

Đã lưu `install_script` và `start_skill` trong bản nháp cấu hình cloud, với
checkout hiện có, công cụ/cache và cách khởi động/readiness. Bản nháp đã lưu
chưa đồng nghĩa được publish hoặc kiểm chứng phục hồi ở task mới.

## Chưa kiểm chứng

- Binary và dịch vụ procd chạy thật trên Kwrt của người dùng.
- Public HTTPS, wildcard DNS/ingress/certificates và việc giữ dịch vụ cũ.
- Upload 50 MiB đi qua Cloudflare, RAM/CPU thực tế và chức năng mạng của router.
- Khởi động lại trên thiết bị, lượng truy cập đồng thời và thử với nhóm người quen.

Tên miền đã điền là `phatnt.id.vn`, trang quản lý `app.phatnt.id.vn` và
`in4` nằm trong danh sách dành riêng. Đã chạy cấu hình production trong cloud
với thư mục/cổng thử riêng: trang quản lý và readiness hoạt động, Host mẫu
cũ bị từ chối, cấu hình trong gói ARM64 khớp với file đã điền.

Những bước chưa kiểm chứng tiếp tục theo `OPENWRT.md` sau khi xác nhận cấu
hình Tunnel thực tế và chuyển artifact. Workflow GitHub Actions đã được
thêm; chưa có kết quả runner GitHub trong báo cáo này. Không có thay đổi
trên router hoặc Cloudflare từ task cloud.
