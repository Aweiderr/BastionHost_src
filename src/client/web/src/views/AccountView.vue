<script setup lang="ts">
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
</script>

<template>
  <div>
    <div class="page-header">
      <div>
        <div class="page-title">修改口令</div>
        <div class="page-subtitle">
          <template v-if="auth.passwordChangeRequired">
            当前口令为临时口令或已过期。完成修改前，其它功能暂不可用。
          </template>
          <template v-else>在此修改登录口令。也可以从右上角「账户」进入。</template>
        </div>
      </div>
    </div>

    <div class="card">
      <div class="card-body">
        <p v-if="auth.passwordChangeRequired">请先修改口令。修改成功后需要使用新口令重新登录。</p>
        <p v-else>修改成功后需要使用新口令重新登录。</p>
        <p v-if="auth.passwordChangeRequired" class="text-secondary" style="margin-top: 8px">
          若暂时不改，可关闭弹窗后从右上角退出登录。
        </p>
        <button class="btn btn-primary mt-16" type="button" @click="auth.openAccountDialog()">
          修改口令
        </button>
      </div>
    </div>
  </div>
</template>
