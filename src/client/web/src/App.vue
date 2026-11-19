<script setup lang="ts">
import AppToast from '@/components/AppToast.vue'
import StepUpHost from '@/components/StepUpHost.vue'
</script>

<template>
  <router-view />
  <AppToast />
  <StepUpHost />
</template>
