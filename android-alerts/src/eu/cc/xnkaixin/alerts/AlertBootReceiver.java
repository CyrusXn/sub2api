package eu.cc.xnkaixin.alerts;

import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;

public final class AlertBootReceiver extends BroadcastReceiver {
    @Override public void onReceive(Context context, Intent intent) {
        if (!Intent.ACTION_BOOT_COMPLETED.equals(intent.getAction())
                && !Intent.ACTION_MY_PACKAGE_REPLACED.equals(intent.getAction())) return;
        if (new AlertSettings(context).hasToken()) {
            try {
                context.startForegroundService(new Intent(context, AlertService.class));
            } catch (IllegalStateException ignored) {
                // 若系统阻止后台启动，用户打开 App 时会重新连接。
            }
        }
    }
}
