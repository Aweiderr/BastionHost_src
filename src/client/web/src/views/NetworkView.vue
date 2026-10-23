<script setup lang="ts">
import NetworkAdvanced from '@/components/NetworkAdvanced.vue'
import NetworkConfig from '@/components/NetworkConfig.vue'

/**
 * 网络配置。
 *
 * 从「系统信息」页独立出来，与系统信息、集群管理同级。理由：
 * 它属于**设备运维**，和系统信息（版本、密码服务、自检历史）不是一类事；
 * 混在一页里既让系统信息页越来越长，也把"改设备网络"这个足以让设备
 * 失联的高风险动作藏在了系统信息下面。
 *
 * 两张卡片分工：
 *   · 网络配置      —— 单个硬件网口的地址 / 网关 / DNS / MTU；
 *   · 高级网络配置  —— 链路聚合 / VLAN 子接口 / 网桥（多文件整体回滚）。
 */
</script>

<template>
  <div>
    <NetworkConfig />
    <NetworkAdvanced />
  </div>
</template>
