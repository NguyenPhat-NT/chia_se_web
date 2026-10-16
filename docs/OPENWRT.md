# Cài GửiWeb trên OpenWrt/Kwrt

Tài liệu này dành cho router JDCloud RE-SS-01 RAM 512 MB có Cloudflare Tunnel
đang public dịch vụ khác. Kết quả chẩn đoán do chủ router cung cấp đã xác nhận
kiến trúc và một số tài nguyên; các lệnh cài bên dưới chưa được thực thi trên
router. Không thay cổng LuCI hoặc ghi đè cấu hình Tunnel hiện có.

## 1. Xác nhận thiết bị trước khi cài

Đăng nhập SSH bằng cách đang dùng và chạy các lệnh không chứa thông tin đăng nhập:

```sh
uname -m
ubus call system board
df -h
free -m
command -v cloudflared
command -v sha256sum
command -v adduser
command -v addgroup
command -v su
```

Kết quả mới nhất do chủ router cung cấp:

| Hạng mục | Đã quan sát |
| --- | --- |
| Kiến trúc hệ điều hành | `aarch64`, dùng binary arm64 |
| Cloudflare Tunnel | `/usr/bin/cloudflared` đang chạy trên cùng router |
| Cổng ứng dụng | 8088 chưa có listener tại lúc kiểm tra |
| Mount ghi được | `/dev/loop0` ở `/overlay`, tổng khoảng 1,9 GiB, trống khoảng 1,8 GiB |
| RAM khả dụng | `MemAvailable` 136.704 KiB, khoảng 133 MiB |
| Swap | 137.212 KiB, khoảng 134 MiB |

Output `free` đã gửi chứa số theo KiB dù lệnh dùng `-m`; kiểm tra đơn vị khi
đọc kết quả và không tính swap như RAM khả dụng. Bộ nhớ trong danh nghĩa 8 GB
chưa tương đương 8 GB dùng được cho ứng dụng: `df` hiện chưa cho thấy mount
riêng có 7 GB trống. Có thể còn dung lượng trên thiết bị chưa được mount, nhưng
cần xác minh trước khi tăng quota. RAM cần đo lại khi GửiWeb chạy cùng Tunnel
và các chức năng mạng.

- Nếu `uname -m` là `aarch64`, dùng `guiweb-linux-arm64`.
- Nếu hệ điều hành là ARM 32-bit hoặc kiến trúc khác, dừng bước cài binary
  arm64; cần build target phù hợp. ARMv8 trong LuCI không chứng minh userspace 64-bit.
- Kiểm tra lại phân vùng bền vững và dung lượng thực trước khi cài. Cấu hình
  mẫu hiện dùng `/overlay/guiweb` với quota nhỏ; đổi sang mount khác nếu đã xác
  minh phù hợp. Không lưu dữ liệu vào `/tmp`.
- Chọn thư mục dữ liệu riêng chỉ dành cho GửiWeb, không dùng lại thư mục có
  file của dịch vụ khác. Ứng dụng dọn staging/release thừa trong thư mục của mình.
- Xác nhận cổng 8088 còn trống bằng `ss -lnt` hoặc `netstat -lnt`, tùy lệnh có sẵn.
- Xác định Tunnel chạy trên router hay một máy khác, và quản lý qua dashboard
  hay file config. Có thể xem `pidof cloudflared`; không cần in toàn bộ dòng
  lệnh tiến trình vì có thể chứa token.

Cần biết tên miền, subdomain quản lý, các subdomain đang dùng, mount dữ liệu
và cách quản lý Tunnel trước khi public. Không gửi mật khẩu/token hoặc nội
dung file credentials vào chat; chỉ cần thông tin kỹ thuật không nhạy cảm.

## 2. Tải gói cài và kiểm tra checksum

Nếu router đã cài v0.1.0–v0.1.2, dùng [hướng dẫn nâng cấp v0.1.3](UPGRADE_0.1.3.md)
để giữ cấu hình, tài khoản và website; không chạy lại bước tạo admin.

Tải trực tiếp bản [v0.1.3 trên GitHub Releases](https://github.com/NguyenPhat-NT/chia_se_web/releases/tag/v0.1.3):

- [guiweb-openwrt-arm64.tar.gz](https://github.com/NguyenPhat-NT/chia_se_web/releases/download/v0.1.3/guiweb-openwrt-arm64.tar.gz).
- [guiweb-openwrt-arm64.tar.gz.sha256](https://github.com/NguyenPhat-NT/chia_se_web/releases/download/v0.1.3/guiweb-openwrt-arm64.tar.gz.sha256).

Dùng thư mục nhận riêng trên router. Ví dụ bên dưới dùng
`/tmp/guiweb-release`; nếu đã có dữ liệu trong thư mục này, chọn thư mục mới
và thay đường dẫn tương ứng trong các bước sau. Chạy tải và kiểm tra:

```sh
mkdir -p /tmp/guiweb-release
cd /tmp/guiweb-release
wget -O guiweb-openwrt-arm64.tar.gz 'https://github.com/NguyenPhat-NT/chia_se_web/releases/download/v0.1.3/guiweb-openwrt-arm64.tar.gz'
wget -O guiweb-openwrt-arm64.tar.gz.sha256 'https://github.com/NguyenPhat-NT/chia_se_web/releases/download/v0.1.3/guiweb-openwrt-arm64.tar.gz.sha256'
sha256sum -c guiweb-openwrt-arm64.tar.gz.sha256
```

Chỉ khi tải thành công và checksum báo `OK` mới giải nén, kiểm tra tiếp binary:

```sh
tar -xzf guiweb-openwrt-arm64.tar.gz -C /tmp/guiweb-release
sha256sum -c guiweb-arm64.sha256
chmod 755 guiweb-linux-arm64
./guiweb-linux-arm64 version
```

Giữ kiểm tra TLS mặc định của `wget`. Nếu tải không được do thiếu CA hoặc
không có tiện ích HTTPS phù hợp, tải hai file trên máy tính rồi chuyển bằng
SCP/SFTP; kiểm tra checksum trên router trước khi giải nén. Không tắt kiểm
tra chứng chỉ để tải. Nếu shell báo lỗi kiến trúc, dừng và kiểm tra lại
`uname -m`; không thử thay thư viện hệ thống.

Hai cách lấy gói thay thế:

- **GitHub Actions:** workflow **GửiWeb - kiểm tra và build** đã chạy thành
  công. Mở repository → **Actions** → lần chạy thành công → tải artifact
  `guiweb-openwrt-arm64`, giải nén ZIP để lấy TAR.GZ. Artifact có hạn giữ
  14 ngày; có thể chạy lại bằng **Run workflow**.
- **Build trong cloud:** dùng checkout hiện có và các lệnh bên dưới, rồi
  chuyển `bin/guiweb-openwrt-arm64.tar.gz` lên router bằng SCP/SFTP.

```sh
cd /workspace/chia_se_web
./scripts/install-dev.sh
./scripts/build.sh
```

Với gói từ Actions hoặc bản vừa build, giải nén trong thư mục nhận riêng và
kiểm tra `guiweb-arm64.sha256` bên trong trước khi dùng binary. Checksum
giúp phát hiện lỗi truyền file; không thay thế xác thực nguồn gửi. Giữ SSH
xác thực host key và chỉ dùng gói từ repository/runner tin cậy.

## 3. Tài khoản dịch vụ và thư mục dữ liệu

Dùng tài khoản `guiweb`, không chạy web bằng root. BusyBox/Kwrt có thể thiếu
tiện ích hoặc có bộ cờ khác; kiểm tra `adduser --help`, `addgroup --help`,
`su --help` trước. Nếu chưa có, cài tiện ích quản lý tài khoản từ feed tin cậy
phù hợp firmware đang dùng; không sửa `/etc/passwd` bằng lệnh ghép chuỗi.

Với BusyBox có các cờ sau, chỉ tạo khi tài khoản/nhóm chưa tồn tại:

```sh
addgroup -S guiweb
adduser -S -D -H -s /bin/false -G guiweb guiweb
```

Không chạy lại các lệnh tạo trên một tài khoản sẵn có. Kiểm tra `id guiweb`,
chọn thư mục riêng dưới mount bền vững đã xác nhận. Với kết quả `/overlay`
hiện tại, kiểm tra lại `df` rồi dùng thư mục sau nếu chưa chứa dữ liệu khác:

```sh
GUIWEB_DATA_DIR='/overlay/guiweb'
df -h "$(dirname "$GUIWEB_DATA_DIR")"
mkdir -p "$GUIWEB_DATA_DIR"
chown guiweb:guiweb "$GUIWEB_DATA_DIR"
chmod 700 "$GUIWEB_DATA_DIR"
mkdir -p /etc/guiweb
chown root:guiweb /etc/guiweb
chmod 750 /etc/guiweb
```

Mặc định production giữ trống 512 MiB, giới hạn tổng dữ liệu 512 MiB và
128 MiB/thành viên. Upload vẫn tối đa 50 MiB, website sau giải nén tối đa
100 MiB. Quota tính cả staging và bản cũ trong lúc cập nhật; mức này cần phù
hợp phân vùng đã chọn. Chưa dùng hạn mức tổng 4 GiB khi mount hiện tại chỉ
có khoảng 1,8 GiB trống.
Không `chown -R` cả mount hoặc các thư mục của dịch vụ khác. Service phải ghi
được thư mục riêng để tự sinh `.secrets-key` quyền 600 trong lần chạy v0.1.3
đầu tiên; không tạo hoặc chép một khóa mã hóa mẫu vào đó. Website do admin
sở hữu dùng ngân sách chung; giới hạn upload, website và khoảng trống vẫn
giữ nguyên. Admin cập nhật website thành viên vẫn theo quota của chủ đó.

## 4. Cấu hình và khởi tạo quản trị viên

Tạo `/etc/guiweb/config.json` từ mẫu sau khi chỉnh:

- `data_dir`: đường dẫn vừa chọn, thuộc tài khoản `guiweb`.
- `admin_host`: đã điền `app.phatnt.id.vn` cho trang quản lý.
- `base_domain`: đã điền `phatnt.id.vn`.
- `reserved`: đã có `in4` để dành riêng cho `in4.phatnt.id.vn`; bổ sung nếu có dịch vụ khác.
- Giữ `dev: false`, `scheme: "https"` khi public.
- Giữ `listen: "127.0.0.1:8088"` nếu cloudflared chạy cùng router.
- `trust_cloudflare: true` chỉ dùng khi cloudflared cùng router, listener là
  IP loopback và không có proxy không tin cậy cùng chuyển tiếp vào đó. Ứng dụng
  dùng `CF-Connecting-IP` để giới hạn thử đăng nhập theo IP người xem, tránh
  tính cả nhóm thành một IP của Tunnel. Không bật tùy chọn này trên listener LAN.

Nếu Tunnel chạy trên máy khác, loopback của router không thể truy cập từ máy
đó: chọn listener trên IP LAN cụ thể và chỉ cho máy Tunnel truy cập bằng
firewall, đặt `trust_cloudflare: false`. Với cách này, giới hạn đăng nhập có thể
gộp người dùng theo IP máy Tunnel; cần đo và điều chỉnh kiến trúc proxy nếu gặp
hạn chế. Không bind `0.0.0.0` rồi mở cổng WAN. Các ví dụ kiểm tra dưới đây
cũng phải đổi địa chỉ listener tương ứng.

Không thay config đang dùng mà chưa giữ bản cũ. Với lần cài đầu, trên router:

```sh
test ! -e /usr/bin/guiweb && cp /tmp/guiweb-release/guiweb-linux-arm64 /usr/bin/guiweb
chmod 755 /usr/bin/guiweb
test ! -e /etc/guiweb/config.json && cp /tmp/guiweb-release/config.example.json /etc/guiweb/config.json
```

Tên miền và thư mục dữ liệu đã điền theo thông tin thiết bị; kiểm tra lại
config trước khi chạy và điều chỉnh nếu cách triển khai thay đổi. File config
không chứa password hoặc Tunnel token. Cấp quyền:

```sh
chown guiweb:guiweb /etc/guiweb/config.json
chmod 600 /etc/guiweb/config.json
```

Tạo admin trong terminal dưới tài khoản dịch vụ để DB/file có đúng chủ sở hữu.
Nếu `su` trên firmware hỗ trợ các cờ này:

```sh
su -s /bin/sh -c '/usr/bin/guiweb admin -config /etc/guiweb/config.json -username admin' guiweb
```

Nhập mật khẩu tại lời nhắc ẩn. Không truyền mật khẩu vào command, biến môi
trường, JSON hay chat. Mật khẩu dài 12–72 byte. Tạo admin trước khi service
chạy; CLI và server không chạy cùng lúc trên một `data_dir`. Nếu `su` không hỗ trợ, dùng công cụ chuyển user đã được
cài và kiểm chứng tương đương; chưa khởi tạo DB bằng root rồi bỏ qua quyền file.

## 5. Chạy riêng ứng dụng và kiểm tra local

Cài script procd vào tên dịch vụ riêng, không ghi đè dịch vụ đang tồn tại:

```sh
test ! -e /etc/init.d/guiweb && cp /tmp/guiweb-release/guiweb.init /etc/init.d/guiweb
chmod 755 /etc/init.d/guiweb
/etc/init.d/guiweb start
```

Service chạy dưới `guiweb`, giới hạn file descriptor, tắt core dump và đặt
`GOMEMLIMIT=64MiB`. Đây là mục tiêu RAM mềm của runtime Go, không phải giới
hạn cứng toàn tiến trình; cần đo thêm SQLite, cloudflared và tải thực tế.

Kiểm tra HTTP nội bộ với Host quản lý đã cấu hình; không bỏ kiểm tra TLS cho
request HTTPS ở bước sau:

```sh
curl --fail --silent --show-error -H 'Host: app.phatnt.id.vn' http://127.0.0.1:8088/api/health
curl --silent --show-error -o /dev/null -w '%{http_code}\n' -H 'Host: unknown.invalid' http://127.0.0.1:8088/
logread -e guiweb
```

Nếu không có curl, dùng tiện ích HTTP có sẵn hỗ trợ Host header tương đương.
Readiness phải thành công với DB và thư mục dữ liệu; Host sai phải trả 404.
Kiểm tra lỗi startup, quyền thư mục và dung lượng nếu readiness chưa thành công.
Không public chỉ vì nhìn thấy PID hoặc cổng đang mở.

## 6. Bổ sung vào Cloudflare Tunnel hiện có

Giữ nguyên Tunnel, credentials và route các dịch vụ đang dùng. Không tạo token
mới chỉ vì chưa thấy token trong môi trường cloud. Không đưa token vào repo.

Nếu quản lý bằng dashboard, thêm Public Hostname cho trang quản lý và wildcard
website, cùng trỏ về listener GửiWeb. Nếu quản lý bằng config local, thêm các
ingress vào config đang có, trước catch-all; giữ route cụ thể đang dùng trước
wildcard. Ví dụ phần bổ sung, không phải toàn bộ file:

```yaml
# Giữ nguyên route in4.phatnt.id.vn và service cũ trước wildcard.
# Chỉ bổ sung hai route bên dưới vào ingress hiện có.
- hostname: app.phatnt.id.vn
  service: http://127.0.0.1:8088
- hostname: "*.phatnt.id.vn"
  service: http://127.0.0.1:8088
# Catch-all hiện có giữ ở cuối, thường là http_status:404.
```

Giữ Host header gốc khi proxy để GửiWeb phân biệt admin và website. Không đặt
một `httpHostHeader` cố định cho wildcard. Tên chưa đăng ký sẽ nhận 404.
Kiểm tra cả `originRequest.httpHostHeader` ở cấp cấu hình chung hoặc Tunnel:
giá trị cố định được kế thừa cũng có thể làm sai Host dù route riêng để trống.
Wildcard phải được khai báo trong DNS và ingress theo chế độ quản lý Tunnel;
với config local, kiểm tra bằng công cụ validate của cloudflared đang cài
trước khi reload. Tránh in file config/credentials có bí mật vào log hay chat.

Wildcard DNS không ghi đè bản ghi cụ thể, nhưng ingress wildcard có thể nhận
lưu lượng của các hostname khác cùng tunnel nếu xếp sai thứ tự. Phải kiểm tra
lại những dịch vụ cũ sau khi thêm route và dành slug của chúng trong `reserved`.

Chứng chỉ edge phải bao phủ `app.phatnt.id.vn` và `tenweb.phatnt.id.vn`.
Nếu dùng `tenweb.sites.phatnt.id.vn`, chứng chỉ `*.phatnt.id.vn` không bao phủ
độ sâu đó; cần kiểm tra/cấu hình chứng chỉ thích hợp, không mặc định sẽ có HTTPS.
Kiểm tra chế độ DNS phù hợp Tunnel và trạng thái chứng chỉ trong Cloudflare.
Không thêm cache rule kiểu cache-everything cho trang quản lý/API; giữ chính
sách cache của ứng dụng để cập nhật cùng link không giữ nội dung cũ.


Tên miền cho lần triển khai này:

| Hostname | Xử lý |
| --- | --- |
| `app.phatnt.id.vn` | HTTP tới `127.0.0.1:8088`, trang quản lý |
| `*.phatnt.id.vn` | HTTP tới `127.0.0.1:8088`, website tải lên |
| `in4.phatnt.id.vn` | Giữ nguyên DNS, route và service hiện có |

Cấu hình GửiWeb dành riêng `in4`: thành viên không thể tạo website có slug đó.
Điều này không tự bảo vệ route Tunnel; vẫn phải giữ route cụ thể của dịch vụ
cũ trước wildcard. Với dashboard, kiểm tra quy tắc cụ thể ưu tiên hơn wildcard
và không sửa service của `in4`. Trong DNS của zone `phatnt.id.vn`, các tên mới
là `app` và `*`; chúng cần trỏ tới Tunnel sử dụng cho GửiWeb, với proxy bật.
Không thay bản ghi `in4` đang hoạt động. Nếu tên `app` hoặc `*` đã có bản ghi,
kiểm tra mục đích trước khi thay đổi.

Gói cài có `tunnel.ingress.fragment.yml` để tham khảo khi dùng config local.
Đây là danh sách bổ sung, không chứa tunnel ID, credentials hoặc route dịch
vụ cũ; không thay toàn bộ config bằng file này.

## 7. Khóa API, Telegram và kiểm chứng trên thiết bị thật

Tab **Khóa API** chỉ quản lý khóa cho website thuộc chính tài khoản đang
đăng nhập. Admin quản lý nội dung site của người khác nhưng không được quản
lý kho khóa của họ. Tối đa 20 tên/site, mẫu `[A-Z][A-Z0-9_]{0,63}`.
**Khóa khác (chỉ lưu giữ)** lưu khóa mã hóa phía server; chưa có proxy API
đa mục đích. **Telegram Bot** lưu bot token và **Chat ID nhận tin** cố định;
nhập chúng trong UI, không đưa vào HTML, shell command, Git, log hoặc prompt.

Relay dùng đường dẫn `POST /__guiweb/telegram/TEN_KHOA` ngay trên hostname
website public, JSON chỉ có `text`. Browser gửi đúng Origin của website;
không mở CORS. Mọi khách truy cập website đều có thể gọi chức năng này,
không có xác thực người gửi riêng. Ratelimit 5 tin/phút/IP khách + site,
20 tin/phút/site, 60 tin/phút chung; tối đa 4 request đồng thời toàn hệ thống,
timeout 10s.
Token, chat đích và URL gọi đi không nhận từ request. Server chỉ gửi văn bản
thường tới API `sendMessage` cố định của Telegram.

Router cần DNS và HTTPS cổng 443 tới `api.telegram.org`, cùng CA hệ thống
hợp lệ. Route wildcard Tunnel hiện có phục vụ cả relay; không mở thêm cổng
WAN. Kiểm thử tự động phải dùng Telegram mock; bot thật do chủ website tự
nhập trong UI khi sử dụng.

DB giữ các khóa dưới dạng AES-GCM, master key nằm ở
`<data_dir>/.secrets-key`, thông thường `/overlay/guiweb/.secrets-key`, mode
600. API không trả giá trị khóa rõ. Khi sao lưu/khôi phục phải giữ DB cùng
file key; mất key khi DB có secret khiến service từ chối khởi động. Không
thay key bằng file mới để vượt lỗi; cần bản sao DB/key tương ứng.


Sau khi local và route đều đúng, truy cập HTTPS bằng trình duyệt rồi:

1. Đăng nhập admin, vào **Lời mời** và tạo link **Nhiều người**. Giao diện
   mặc định 20 lượt, cho phép 1–1000; hơn một lượt có hạn 7 ngày, một lượt
   có hạn 48 giờ. Dùng cùng link tạo hai tài khoản thành viên, kiểm tra số
   đăng ký thành công và lượt còn lại. Người nhận có thể chuyển tiếp link.
   Kiểm tra hết lượt, hết hạn và thu hồi đều chặn đăng ký mới; các tài khoản
   tạo từ link không có quyền admin.
2. Chọn HTML đơn hoặc ZIP có CSS/JS bằng nút chọn file, và chọn thư mục có
   đường dẫn tài nguyên tuyệt đối bằng nút chọn thư mục. Giao diện phải tự
   nhận dạng nội dung; không yêu cầu chọn HTML/ZIP/thư mục bằng radio.
3. Mở từng link bằng người xem không đăng nhập, kiểm tra CSS/JS tải đúng.
4. Đổi tên và cập nhật: link giữ nguyên, nội dung mới xuất hiện; upload lỗi giữ bản cũ.
5. Kiểm tra thành viên không quản lý được website của người khác, xóa thu hồi link.
   Sau khi xóa, thử tạo lại cùng slug bằng tài khoản khác: phải thành công
   và link cũ hiển thị website mới. Tên đang dùng vẫn duy nhất; các tên dành
   riêng như `app`, `in4` vẫn bị chặn.
6. Thử một upload hợp lệ gần 50 MiB; giới hạn Cloudflare tính cả multipart nên
   cần lớn hơn tổng file một chút. Giới hạn/timeout phụ thuộc cấu hình thực tế.
7. Kiểm tra CPU/RAM/disk khi upload và xem website cùng lúc, đo chức năng mạng.
8. Restart riêng GửiWeb, kiểm tra dữ liệu còn nguyên và link vẫn phục vụ đúng.
9. Kiểm tra lại `in4.phatnt.id.vn` và tất cả dịch vụ public cũ.
10. Chọn **Dán HTML**, tạo một website rồi cập nhật HTML và kiểm tra link
    giữ nguyên; bản HTML thay toàn bộ file cũ. Tab **Tạo với AI** chỉ tạo
    prompt/sao chép clipboard, không tự gọi AI hoặc chạy backend tùy ý.
11. Kiểm tra tab **Khóa API** chỉ liệt kê site của chính chủ. Cấu hình khóa
    bằng UI; danh sách chỉ trả metadata, thao tác thay không hiển thị lại
    giá trị cũ. Kiểm tra quyền bằng hai tài khoản khác nhau.
12. Khi kiểm chứng relay tự động, dùng mock để xác nhận chat đích cố định,
    Origin và các rate limit; không dùng token thật hoặc gửi tin ngoài ý muốn.

Sau khi các bước cần thiết đều thành công:

```sh
/etc/init.d/guiweb enable
```

Chưa cam kết mức truy cập đồng thời khi chưa có số đo. Bắt đầu với 2–3 người
dùng và website nhỏ. Nếu thiếu RAM/CPU, hạ hạn mức sau khi đo; không tắt kiểm
tra upload hoặc xác thực để làm cho test vượt qua.

Xóa trên router không xóa `localStorage` hoặc service worker đã lưu trên
trình duyệt. Khi slug được dùng lại, người xem có thể cần xóa dữ liệu trang
để loại bỏ dữ liệu/cache cũ.

## 8. Nâng cấp, quên mật khẩu và quay lại bản cũ

Từ v0.1.0–v0.1.2 lên v0.1.3, xem [các lệnh nâng cấp và quay lại](UPGRADE_0.1.3.md).
Bản mới bổ sung bảng khóa mã hóa, giữ schema version 1, config và dữ liệu cũ.
Service tự sinh `.secrets-key` trong data_dir ở lần chạy đầu nếu chưa có
secret; thư mục dữ liệu hiện có phải ghi được bởi tài khoản service.

Quay lại binary cũ không cần khôi phục DB; bản cũ không dùng vault hoặc file
key. Giữ `.secrets-key` nguyên vẹn để chạy lại v0.1.3 và giải mã các khóa đã
lưu. Nếu quay về v0.1.0/v0.1.1, link đăng ký nhiều lượt cũng tạm không dùng
được; số lượt vẫn được giữ và dùng tiếp trên bản mới nếu còn hạn/lượt.
Các hướng dẫn [v0.1.1](UPGRADE_0.1.1.md), [v0.1.2](UPGRADE_0.1.2.md) giữ lại
để tham khảo lịch sử.

Sau nâng cấp, tải lại trang quản lý một lần bằng **Ctrl+Shift+R**. v0.1.2
dùng URL JS/CSS theo nội dung để tránh HTML mới đi cùng JS cũ trong cache,
nguyên nhân khiến nút tải lên không phản hồi. Sửa cache và đổi quota admin
là hai thay đổi riêng.

Quên mật khẩu: chạy CLI trong terminal dưới tài khoản dịch vụ, thay `admin`
bằng tài khoản cần đặt lại. Dừng riêng GửiWeb trước vì ứng dụng khóa thư mục
dữ liệu để ngăn nhiều tiến trình cùng sửa DB/file; không dừng Tunnel hoặc LuCI:

```sh
/etc/init.d/guiweb stop
su -s /bin/sh -c '/usr/bin/guiweb reset-password -config /etc/guiweb/config.json -username admin' guiweb
/etc/init.d/guiweb start
```

Trước nâng cấp, kiểm tra checksum binary mới, giữ binary/config cũ và dừng
riêng service GửiWeb. Khi cần giữ DB trước migration, dùng backup SQLite nhất
quán hoặc dừng service hoàn toàn và giữ cả dữ liệu DB liên quan; không copy
một file DB đang hoạt động trong WAL mode rồi coi là backup đầy đủ. Backup
của v0.1.3 còn phải chứa `.secrets-key` tương ứng; cách đơn giản là dừng
service và lưu cả thư mục dữ liệu, bao gồm file ẩn.
Chọn tên bản lưu chưa tồn tại, chỉ thay binary sau khi xác nhận bản cũ còn giữ.

Chạy readiness và một website thật sau nâng cấp. Nếu lỗi, dừng GửiWeb, khôi
phục binary/config và DB tương thích rồi chạy lại. Thu hồi riêng các route mới
của GửiWeb nếu cần; không xóa Tunnel hoặc route dịch vụ khác.

Không có sao lưu tự động trong bản đầu. Bộ nhớ hỏng hoặc cài lại router có
thể làm mất toàn bộ website và tài khoản; cân nhắc sao lưu thủ công khi dữ liệu
bắt đầu quan trọng. Binary cross-build và test cloud chỉ chuẩn bị artifact,
không thay thế kết quả chạy thật trên router.
