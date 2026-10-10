# GửiWeb

Chia sẻ website tĩnh cho một nhóm người quen: tải HTML, ZIP hoặc thư mục lên,
nhận link public dạng `tenweb.phatnt.id.vn`. Thiết kế cho router OpenWrt/Kwrt
512 MB RAM, dùng Cloudflare Tunnel đang có.

## Bản đầu

- Quản trị viên tạo link mời; người nhận tự chọn tài khoản và mật khẩu.
- Thành viên quản lý website của mình; quản trị viên quản lý tất cả.
- Upload một file HTML, ZIP hoặc thư mục, tối đa 50 MiB dữ liệu mỗi lượt.
- Đổi tên hiển thị, cập nhật nội dung giữ nguyên link, xóa và sao chép link.
- Slug đã xóa được giữ lại, không cấp cho website khác để link cũ không trỏ
  sang nội dung mới.
- Website chỉ chứa HTML/CSS/JavaScript và tài nguyên tĩnh. React/Vue phải được
  build trước khi upload; PHP, Node.js backend và database của website không chạy.
- Website có thể dùng `localStorage`; dữ liệu riêng trên từng trình duyệt.
- SPA có tùy chọn fallback về `index.html`; bật khi cần điều hướng bằng JS.
- Giao diện tiếng Việt, responsive, có chế độ sáng/tối.

Mặc định: 100 MiB sau giải nén, 5.000 file/website, 128 MiB/thành viên,
512 MiB tổng dữ liệu và production giữ trống ít nhất 512 MiB. Một lượt upload
được xử lý tại một thời điểm. Các mức này dựa trên kết quả chủ router cung cấp:
`/overlay` hiện có khoảng 1,8 GiB trống, chưa thấy mount riêng có 7 GB khả dụng.
Cấu hình có thể tăng sau khi xác nhận thêm phân vùng lưu trữ và đo tài nguyên.

## Phát triển trong cloud

Dùng checkout hiện có; mỗi task cloud đã được cách ly, không cần tạo worktree.
Go 1.27.2 được pin; Node.js và Docker không bắt buộc.

```sh
cd /workspace/chia_se_web
./scripts/install-dev.sh
./scripts/build.sh
```

Script cài SDK Linux amd64 vào `/workspace/toolchains/go`, kiểm tra TLS và
SHA-256, giữ nguyên SDK khác nếu đường dẫn đã tồn tại. Dependency được tải với
`-mod=readonly`; không tự sửa `go.mod`/`go.sum`. Cache nằm ngoài checkout.
Muốn dùng workspace khác, đặt `GUIWEB_WORKSPACE`; muốn dùng SDK đã cài, đặt
`GUIWEB_GO_ROOT`. SDK phải đúng phiên bản đã pin.

Chạy test:

```sh
export PATH="/workspace/toolchains/go/bin:$PATH"
export GOCACHE=/workspace/.cache/guiweb-go
export GOPATH=/workspace/.cache/guiweb-gopath
export GOTOOLCHAIN=local
go test -mod=readonly ./...
```

Tạo quản trị viên trong terminal, nhập mật khẩu tại lời nhắc:

```sh
./bin/guiweb-linux-amd64 admin -dev -data-dir /tmp/guiweb-dev -username admin
./bin/guiweb-linux-amd64 serve -dev -data-dir /tmp/guiweb-dev -listen 127.0.0.1:8088
```

Mật khẩu dài 12–72 byte. Tạo admin hoặc đặt lại mật khẩu cần dừng tiến trình
GửiWeb đang dùng cùng `data_dir` trước; ứng dụng khóa thư mục để ngăn hai
tiến trình sửa dữ liệu đồng thời.

Development dùng host quản lý `app.localhost`, website `tenweb.localhost`, HTTP
và cookie dành riêng cho development. Dữ liệu trong `/tmp` có thể mất khi máy
khởi động lại; dùng thư mục ngoài checkout nếu cần giữ lâu dài. Không bật `-dev`
trên router public.

Kiểm tra nội bộ bằng request local, không cần public môi trường cloud:

```sh
curl --fail --silent --show-error -H 'Host: app.localhost' http://127.0.0.1:8088/api/health
```

## Build và cài lên router

Tải trực tiếp bản [v0.1.0 trên GitHub Releases](https://github.com/NguyenPhat-NT/chia_se_web/releases/tag/v0.1.0):

- [Gói cài OpenWrt ARM64](https://github.com/NguyenPhat-NT/chia_se_web/releases/download/v0.1.0/guiweb-openwrt-arm64.tar.gz).
- [SHA-256 của gói](https://github.com/NguyenPhat-NT/chia_se_web/releases/download/v0.1.0/guiweb-openwrt-arm64.tar.gz.sha256).

Có thể dùng `wget` trên router để tải hai file, kiểm tra SHA-256 trước khi
giải nén; lệnh đầy đủ nằm trong [hướng dẫn OpenWrt](docs/OPENWRT.md#2-tải-gói-cài-và-kiểm-tra-checksum).
Gói gồm binary, checksum, cấu hình và script dịch vụ; chưa tự cài lên router.

Nếu muốn tự build trong checkout:

`./scripts/build.sh` tạo binary Linux amd64 và Linux arm64 thuần Go, không
phụ thuộc CGO, cùng `bin/SHA256SUMS` và gói `bin/guiweb-openwrt-arm64.tar.gz`
chứa binary, checksum, cấu hình và script dịch vụ. Router hiện đã xác nhận `uname -m` là
`aarch64`, phù hợp bản arm64. Khi cài trên thiết bị khác, kiểm tra kiến trúc hệ
điều hành trước khi chọn binary.

Xem [hướng dẫn OpenWrt](docs/OPENWRT.md) để chọn phân vùng lưu trữ, tạo tài
khoản dịch vụ, cài procd và bổ sung route vào Tunnel hiện có. Cấu hình mẫu ở
[deploy/config.example.json](deploy/config.example.json), dịch vụ mẫu ở
[deploy/guiweb.init](deploy/guiweb.init).

Cấu hình router đã điền `app.phatnt.id.vn` cho trang quản lý và
`phatnt.id.vn` cho website chia sẻ. Tên `in4` được dành riêng cho dịch vụ
`in4.phatnt.id.vn` hiện có. Hai route bổ sung nằm trong
[deploy/tunnel.ingress.fragment.yml](deploy/tunnel.ingress.fragment.yml);
không dùng đoạn này thay toàn bộ cấu hình Tunnel. Route của `in4` phải giữ
nguyên và đứng trước wildcard khi cùng nằm trong một ingress.

Workflow **GửiWeb - kiểm tra và build** đã chạy thành công trên GitHub: kiểm
tra test/race/vet, build và tạo artifact `guiweb-openwrt-arm64`. Đây là cách
lấy gói thay thế: mở tab **Actions**, chọn lần chạy thành công rồi tải artifact
ZIP và lấy file TAR.GZ bên trong. Artifact được giữ 14 ngày; có thể chạy lại
bằng **Run workflow**. Workflow phát hành chạy thủ công để tạo bản trên
GitHub Releases sau khi kiểm tra và build.

Trang quản lý và từng website dùng hostname riêng. Cookie quản lý không chia
sẻ cho các subdomain; API kiểm tra quyền, Origin và CSRF. Nội dung upload có
thể chạy JavaScript trên trình duyệt, vì vậy chỉ mời người quen đáng tin cậy.

## Vận hành

- Link mời là thông tin truy cập: tự gửi cho người nhận, không đưa lên Git hoặc log.
- Quên mật khẩu: chủ router dùng `guiweb reset-password -config <path> -username <name>`
  trong terminal sau khi dừng riêng dịch vụ GửiWeb, rồi khởi động lại dịch vụ;
  mật khẩu không nằm trong tham số dòng lệnh.
- Giữ dung lượng cho cả bản website cũ và mới trong lúc cập nhật.
- Không có sao lưu tự động; bộ nhớ hỏng hoặc cài lại router có thể mất dữ liệu.
- Build thành công chưa chứng minh ứng dụng chạy trên router; cần kiểm tra local,
  HTTPS, upload, restart và đo RAM/CPU/disk trên thiết bị thật.

[Kế hoạch ban đầu](docs/KE_HOACH_TRIEN_KHAI.md) được giữ làm tài liệu thiết kế;
phần hiện trạng trong kế hoạch phản ánh thời điểm trước khi có ứng dụng.
