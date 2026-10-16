# GửiWeb

Chia sẻ website tĩnh cho một nhóm người quen: tải HTML, ZIP hoặc thư mục lên,
nhận link public dạng `tenweb.phatnt.id.vn`. Thiết kế cho router OpenWrt/Kwrt
512 MB RAM, dùng Cloudflare Tunnel đang có.

## Bản đầu

- Quản trị viên tạo link đăng ký dùng chung hoặc một lần; người nhận tự tạo
  tài khoản thành viên và có thể chuyển tiếp link cho bạn bè.
- Thành viên quản lý website của mình; admin quản lý nội dung tất cả website.
  Kho khóa API chỉ chủ website được quản lý, kể cả khi người khác là admin.
- Tự nhận dạng HTML, ZIP hoặc nội dung thư mục sau khi chọn file/thư mục;
  tối đa 50 MiB dữ liệu mỗi lượt, không cần chọn loại upload thủ công.
- Dán HTML để tạo hoặc cập nhật website; sao chép prompt để tự dùng với AI.
- Lưu khóa API phía server; có relay Telegram dùng bot và chat đã cấu hình.
- Đổi tên hiển thị, cập nhật nội dung giữ nguyên link, xóa và sao chép link.
- Tên đang dùng là duy nhất; sau khi xóa website, bất kỳ thành viên nào cũng
  dùng lại được slug đó. Các tên dành riêng như `app`, `in4` vẫn bị chặn.
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
Website do admin sở hữu dùng ngân sách chung, không chịu quota cá nhân 128 MiB;
các giới hạn upload, website, tổng dung lượng và bộ nhớ trống vẫn áp dụng.
Admin cập nhật website của thành viên vẫn giữ chủ sở hữu và quota của người đó.

## Link đăng ký

Trong tab **Lời mời**, admin chọn **Nhiều người**, nhập số lượt rồi tạo link.
Giao diện mặc định 20 lượt; cho phép số nguyên 1–1000. Link có hơn một lượt
hết hạn sau 7 ngày; chọn **Một người** hoặc 1 lượt thì hết hạn sau 48 giờ.
Người nhận có thể chuyển tiếp link để bạn bè tự đăng ký; các tài khoản mới
đều là thành viên, không được cấp quyền admin.

Danh sách hiển thị số đăng ký thành công, số lượt còn lại, thời hạn và trạng
thái. Link ngừng nhận đăng ký khi hết lượt, hết hạn hoặc bị admin thu hồi.
Thu hồi không xóa các tài khoản đã đăng ký. Sao chép link ngay sau khi tạo
và gửi qua kênh bạn đang dùng.

## Dán HTML và tạo với AI

Trong hộp tải lên, chọn **Dán HTML**, dán **Mã HTML đầy đủ** rồi tạo hoặc
cập nhật website. Nội dung được lưu thành `index.html` và áp dụng các hạn
mức upload hiện có. Khi cập nhật, link giữ nguyên nhưng toàn bộ bộ file cũ
được thay bằng HTML mới; CSS/JS/ảnh từ bản trước không tự giữ lại. Dùng HTML
có CSS/JS bên trong, hoặc upload ZIP/thư mục khi cần nhiều file.

Tab **Tạo với AI** có ô **Bạn muốn làm website gì?**, nút **Tạo lại prompt**
và **Sao chép prompt**. Bạn tự dán prompt vào công cụ AI mình chọn, rồi dán
HTML kết quả vào GửiWeb. Nút chỉ tạo/sao chép văn bản trên trình duyệt; GửiWeb
không tích hợp gọi AI trả phí hoặc chạy backend tùy ý từ mã được tạo.

## Khóa API và Telegram

Trong tab **Khóa API**, chọn **Website của bạn**, đặt **Tên khóa**, loại và
**Giá trị khóa mới**, rồi **Lưu khóa**. Chỉ chủ website được tạo, thay hoặc
thu hồi khóa; admin không quản lý kho khóa của website người khác. Mỗi site
có tối đa 20 tên, theo mẫu `[A-Z][A-Z0-9_]{0,63}`: chữ cái viết hoa đầu tiên,
sau đó là chữ hoa, số hoặc `_`, tổng tối đa 64 ký tự.

Giá trị được mã hóa AES-GCM trong SQLite; API không trả lại khóa dạng rõ.
**Thay khóa** yêu cầu nhập giá trị mới; **Thu hồi** ngừng dùng cấu hình đó.
Loại **Khóa khác (chỉ lưu giữ)** là kho lưu phía server, chưa có proxy gọi
API tùy ý hoặc cách nhúng khóa đó vào JavaScript public.

Với **Telegram Bot**, nhập bot token trong UI và **Chat ID nhận tin** cố
định. Token lấy từ bot do bạn tạo với `@BotFather`. Với chat cá nhân, mở bot
và nhấn **Start** trước, rồi dùng Chat ID dạng số. Với nhóm/kênh, thêm bot
và cấp quyền gửi tin; `@ten_kenh` chỉ dùng cho kênh công khai.
Chọn **Sao chép mã gửi tin** hoặc gọi từ chính website đã public:

```js
await fetch('/__guiweb/telegram/CONTACT', {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ text: 'Có lời nhắn mới từ website.' })
});
```

Thay `CONTACT` bằng tên khóa đã lưu. Request chỉ gửi `text`; bot token và
chat đích được lấy từ server. Relay chỉ gọi phương thức `sendMessage` trên
`api.telegram.org` với văn bản thường, không nhận token, chat đích hoặc URL
tùy ý từ client.
Browser phải gửi đúng Origin của website; server không mở CORS cho site khác.

Endpoint này công khai với khách truy cập website, không xác thực riêng
người gửi. Origin không phải cơ chế xác thực. Giới hạn: 5 tin/phút cho mỗi
cặp IP khách + website, 20 tin/phút/website, 60 tin/phút toàn hệ thống, tối
đa 4 request Telegram đồng thời toàn hệ thống, timeout 10 giây. Các mức này
giúp giảm spam.
Router cần truy cập HTTPS tới `api.telegram.org`; không đặt token vào HTML,
Git, log hoặc prompt AI. Khi kiểm thử tự động, dùng Telegram mock.

File khóa mã hóa nằm ở `<data_dir>/.secrets-key`, quyền 600, được service
sinh trong lần chạy đầu. Giữ file này cùng DB khi sao lưu/khôi phục. Mất file
khi đã lưu secret khiến các khóa không giải mã được và service từ chối khởi
động; không tạo file thay thế để bỏ qua lỗi. Xem thêm [hướng dẫn OpenWrt](docs/OPENWRT.md).

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

Tải trực tiếp bản [v0.1.3 trên GitHub Releases](https://github.com/NguyenPhat-NT/chia_se_web/releases/tag/v0.1.3):

- [Gói cài OpenWrt ARM64](https://github.com/NguyenPhat-NT/chia_se_web/releases/download/v0.1.3/guiweb-openwrt-arm64.tar.gz).
- [SHA-256 của gói](https://github.com/NguyenPhat-NT/chia_se_web/releases/download/v0.1.3/guiweb-openwrt-arm64.tar.gz.sha256).

Có thể dùng `wget` trên router để tải hai file, kiểm tra SHA-256 trước khi
giải nén; lệnh đầy đủ nằm trong [hướng dẫn OpenWrt](docs/OPENWRT.md#2-tải-gói-cài-và-kiểm-tra-checksum).
Gói gồm binary, checksum, cấu hình và script dịch vụ; chưa tự cài lên router.
Trong gói có `UPGRADE_0.1.3.md` để đọc các lệnh nâng cấp ngoại tuyến.

Đang dùng v0.1.0–v0.1.2: xem [hướng dẫn nâng cấp v0.1.3](docs/UPGRADE_0.1.3.md).
Giữ tài khoản, cấu hình, DB, website và `.secrets-key` nếu đã có; không tạo
lại admin. Các hướng dẫn [v0.1.1](docs/UPGRADE_0.1.1.md) và
[v0.1.2](docs/UPGRADE_0.1.2.md) được giữ để tham khảo lịch sử.

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

- Link đăng ký có thể chuyển tiếp cho bạn bè trong số lượt và thời hạn đã
  đặt; admin có thể thu hồi để ngừng đăng ký mới. Không đưa token vào Git/log.
- Quên mật khẩu: chủ router dùng `guiweb reset-password -config <path> -username <name>`
  trong terminal sau khi dừng riêng dịch vụ GửiWeb, rồi khởi động lại dịch vụ;
  mật khẩu không nằm trong tham số dòng lệnh.
- Giữ dung lượng cho cả bản website cũ và mới trong lúc cập nhật.
- Sau khi slug được dùng lại, link cũ hiển thị website mới. Xóa trên router
  không xóa `localStorage` hoặc service worker của hostname trên trình duyệt;
  người xem có thể cần xóa dữ liệu trang nếu nội dung cũ còn xuất hiện.
- Không có sao lưu tự động; bộ nhớ hỏng hoặc cài lại router có thể mất dữ liệu.
  Khi sao lưu thủ công, dừng service và giữ toàn bộ thư mục dữ liệu, gồm DB
  và `.secrets-key`; không chỉ copy riêng SQLite khi đang chạy WAL.
- Build thành công chưa chứng minh ứng dụng chạy trên router; cần kiểm tra local,
  HTTPS, upload, restart và đo RAM/CPU/disk trên thiết bị thật.

[Kế hoạch ban đầu](docs/KE_HOACH_TRIEN_KHAI.md) được giữ làm tài liệu thiết kế;
phần hiện trạng trong kế hoạch phản ánh thời điểm trước khi có ứng dụng.
