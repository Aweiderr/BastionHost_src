# 密码机（SDF / GM/T 0018）

堡垒机默认使用软件密码模块（`BASTION_CRYPTO_PROVIDER=soft`）。要让密码机或密码卡参与实际运算，把实现改成 `sdf`。程序在**运行时** `dlopen` 厂商动态库，编译不链接 `libsvc.so`，仓库里也不存放厂商源码或 `.so`。

本实现按厂商头文件的坐标宽度编组：`ECCref_MAX_BITS = 512`（坐标 64 字节，右对齐）。`SDF_OpenDevice` 不接收地址和端口，设备地址由厂商库自己的配置决定（现场常见为 `/opt/hsm` 下的 `ccit_tcp_server` 与 `libsdf_crypto.so`）。

## 部署

1. 把厂商提供的 `libsvc.so`（或设备上的 `libsdf_crypto.so`）放到堡垒机主机，例如 `/opt/hsm/libs/libsvc.so`。不要把它提交进本仓库。
2. 若该库还依赖同目录下的其他 `.so`，把目录加入 `LD_LIBRARY_PATH`。
3. 在密码机上预先生成：
   - SM2 签名密钥（以及需要解密时的加密密钥），索引默认 `1`；
   - KEK，索引默认 `1`。SM4 托管密钥只保存 KEK 包装后的密钥包，明文密钥不进入堡垒机进程。
4. 设置环境变量后重启 **manager** 与 **server**（两边都要设，录像加密在 server）：

```bash
export BASTION_CRYPTO_PROVIDER=sdf
export BASTION_SDF_LIB=/opt/hsm/libs/libsvc.so
export BASTION_SDF_PIN='私钥访问控制码'
export BASTION_SDF_SM2_INDEX=1
export BASTION_SDF_KEK_INDEX=1
# 可选
export BASTION_SDF_ALG_PROFILE=gmt0006   # 旧固件可改为 legacy
export BASTION_SDF_SM2_HASH=device       # 设备不做 SM2 预处理时改为 soft
export BASTION_SDF_ALLOW_KEYGEN=false    # 见下文，生产保持 false
```

`BASTION_SDF_ALLOW_KEYGEN=true` 会调用 `EVDF_CreateKeyPair_ECC`，**覆盖**该索引上的原密钥。只在空索引的实验环境使用。

## 运算对应关系

| 堡垒机接口 | SDF |
| --- | --- |
| `Random` | `SDF_GenerateRandom` |
| `SM3Sum` / `SM3New` | `SDF_HashInit/Update/Final`（`SGD_SM3`）。设备失败时回退软件 SM3，下一次自检会标出 |
| `SM4Encrypt` / `SM4Decrypt` | `SDF_ImportKey` + `SDF_Encrypt` / `SDF_Decrypt`。PKCS#7 由堡垒机填充，设备做裸分组运算。不提供 GCM |
| `SM2Sign` / `SM2Verify` / `SM2Encrypt` / `SM2Decrypt` | 外部密钥：`SDF_External*_ECC`。摘要优先在设备上做（`SDF_HashInit` 带公钥与默认用户标识 `1234567812345678`） |
| `KeyGenerate`（SM2） | 绑定已有索引，或在允许时 `EVDF_CreateKeyPair_ECC`。目录里只有公钥和索引 |
| `KeySign` | `SDF_GetPrivateKeyAccessRight` + `SDF_InternalSign_ECC` |
| `KeyEncrypt` / `KeyDecrypt`（SM2） | `SDF_InternalEncrypt_ECC` / `SDF_InternalDecrypt_ECC` |
| `KeyGenerate`（SM4） | `SDF_GenerateKeyWithKEK`，只落盘包装结果 |
| `KeyEncrypt` / `KeyDecrypt`（SM4） | `SDF_ImportKeyWithKEK` + `SDF_Encrypt` / `SDF_Decrypt`，密文格式仍是 `IV ‖ CBC` |
| `SelfTest` | 设备信息、SM3 标准向量 `abc`、SM4 往返、SM2 往返、随机数；配置了 PIN 时再做内部签名；并用 KEK 做一次会话密钥往返 |
| RSA / HMAC | 仍由软件模块托管。本版不把 RSA 私钥写入密码机 |

`SM2GenerateKey` 会把外部密钥对交回调用方，这是接口本身的约定。托管路径（`KeySign` / `KeyDecrypt`）不导出私钥。

## 旧密文

切换到 `sdf` 后，已经由 `soft` 写入密钥库的密钥 ID 仍由软件模块解密，**不迁移、不重加密**。新的 `KeyGenerate` 才走密码机。`km_key.provider` 记录密钥当时的实现。

历史录像默认用 `record-verify -provider soft`。若录像是在 `sdf` 下封存的：

```bash
record-verify -file ./data/records/...bsr -provider sdf \
  -sdf-lib /opt/hsm/libs/libsvc.so -sdf-pin "$BASTION_SDF_PIN" \
  -sdf-kek-index 1 -keystore ./data/server-keys
```

## 如何确认接上了

启动日志里应出现 `provider=sdf` 以及库路径、索引。管理端开机自检和 `POST /api/v1/system/selftest`（需登录）会跑 `Provider.SelfTest`。`GET /api/v1/system/crypto/selftest` 返回分项：设备在位、SM3 向量、SM4、SM2、随机数、内部私钥、KEK。

没有密码机时：

```bash
cd src/shared
CGO_ENABLED=0 go test ./crypto/...
CGO_ENABLED=1 go test ./crypto/...
```

这两条都不需要 `libsvc.so`。`provider=sdf` 且路径错误时初始化失败；不设置该变量时行为与原来的 soft 相同。

真机还需要现场确认：厂商库实际导出的算法标识（不对时把 `BASTION_SDF_ALG_PROFILE` 改成 `legacy`）、`SDF_HashInit` 是否按默认用户标识做 ZA（不对时把 `BASTION_SDF_SM2_HASH` 设为 `soft`，私钥运算仍在设备内）、以及索引上的签名密钥、加密密钥和 KEK 是否已经灌好。
