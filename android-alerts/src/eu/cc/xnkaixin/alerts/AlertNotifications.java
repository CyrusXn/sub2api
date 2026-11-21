package eu.cc.xnkaixin.alerts;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.content.Context;
import android.content.Intent;
import android.graphics.Color;
import android.media.AudioAttributes;
import android.provider.Settings;

final class AlertNotifications {
    static final String BALANCE = "balance_urgent";
    static final String RATE = "multiplier_urgent";
    private static final String SERVICE = "monitor_status";

    static void createChannels(Context context) {
        NotificationManager manager = manager(context);
        manager.createNotificationChannel(new NotificationChannel(SERVICE, "监控状态", NotificationManager.IMPORTANCE_LOW));
        createUrgentChannel(manager, BALANCE, "余额不足");
        createUrgentChannel(manager, RATE, "倍率变化");
    }

    private static void createUrgentChannel(NotificationManager manager, String id, String title) {
        NotificationChannel channel = new NotificationChannel(id, title, NotificationManager.IMPORTANCE_HIGH);
        channel.enableVibration(true);
        channel.setVibrationPattern(new long[]{0, 300, 160, 300});
        channel.setSound(Settings.System.DEFAULT_NOTIFICATION_URI, new AudioAttributes.Builder()
                .setUsage(AudioAttributes.USAGE_NOTIFICATION).build());
        channel.setLockscreenVisibility(Notification.VISIBILITY_PUBLIC);
        manager.createNotificationChannel(channel);
    }

    static Notification serviceNotification(Context context) {
        return new Notification.Builder(context, SERVICE)
                .setSmallIcon(R.drawable.ic_alert)
                .setContentTitle("Sub2API 告警监控")
                .setContentText("每 30 秒检查余额和倍率告警")
                .setContentIntent(openApp(context)).setOngoing(true).build();
    }

    static boolean canNotify(Context context, String channelId) {
        NotificationManager manager = manager(context);
        if (!manager.areNotificationsEnabled()) return false;
        if (channelId == null) return true;
        NotificationChannel channel = manager.getNotificationChannel(channelId);
        return channel != null && channel.getImportance() != NotificationManager.IMPORTANCE_NONE;
    }

    static void show(Context context, String id, String kind, String title, String body) {
        String channel = "multiplier_changed".equals(kind) ? RATE : BALANCE;
        Notification notification = new Notification.Builder(context, channel)
                .setSmallIcon(R.drawable.ic_alert).setColor(Color.rgb(220, 74, 63))
                .setContentTitle(title).setContentText(body)
                .setStyle(new Notification.BigTextStyle().bigText(body))
                .setContentIntent(openApp(context)).setAutoCancel(true).setOnlyAlertOnce(true)
                .setCategory(Notification.CATEGORY_ERROR).setVisibility(Notification.VISIBILITY_PUBLIC).build();
        manager(context).notify(id, 2, notification);
    }

    private static NotificationManager manager(Context context) {
        return context.getSystemService(NotificationManager.class);
    }

    private static PendingIntent openApp(Context context) {
        return PendingIntent.getActivity(context, 0, new Intent(context, MainActivity.class),
                PendingIntent.FLAG_IMMUTABLE | PendingIntent.FLAG_UPDATE_CURRENT);
    }
}
