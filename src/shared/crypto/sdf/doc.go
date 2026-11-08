// Package sdf 通过 GM/T 0018 SDF 接口使用密码机 / 密码卡。
//
// 厂商动态库（libsvc.so，或设备上的 libsdf_crypto.so）在运行时 dlopen，
// 不参与编译、不进入仓库。未设置 BASTION_CRYPTO_PROVIDER=sdf 时，
// 本包不会被工厂打开，默认软件实现与缺少 .so 的构建都不受影响。
//
// 设备连接参数由厂商库自己的配置决定：标准 SDF_OpenDevice 不接收地址和端口。
package sdf
