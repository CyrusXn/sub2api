package eu.cc.xnkaixin.alerts;

import android.app.Service;
import android.content.Intent;
import android.os.IBinder;
import android.os.PowerManager;
import android.os.SystemClock;

import org.json.JSONArray;
import org.json.JSONObject;

import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.ScheduledThreadPoolExecutor;
import java.util.concurrent.TimeUnit;
import javax.net.ssl.HttpsURLConnection;

public final class AlertService extends Service {
    private static final long POLL_SECONDS = 30;
    private final ScheduledThreadPoolExecutor worker = new ScheduledThreadPoolExecutor(1);
    private AlertSettings settings;
    private volatile int generation;
    private volatile HttpsURLConnection activeRequest;
    private java.util.concurrent.ScheduledFuture<?> polling;

    @Override public IBinder onBind(Intent intent) {
        return null;
    }

    @Override public void onCreate() {
        super.onCreate();
        settings = new AlertSettings(this);
        AlertNotifications.createChannels(this);
        worker.setRemoveOnCancelPolicy(true);
    }

    @Override public int onStartCommand(Intent intent, int flags, int startId) {
        startForeground(1, AlertNotifications.serviceNotification(this));
        if (polling != null) polling.cancel(false);
        int currentGeneration = ++generation;
        polling = worker.scheduleWithFixedDelay(() -> check(currentGeneration), 0, POLL_SECONDS, TimeUnit.SECONDS);
        return START_STICKY;
    }

    private void check(int currentGeneration) {
        if (generation != currentGeneration) return;
        if (!AlertNotifications.canNotify(this, AlertNotifications.BALANCE)
                || !AlertNotifications.canNotify(this, AlertNotifications.RATE)) {
            status("请在系统中允许余额不足和倍率变化通知");
            return;
        }
        PowerManager.WakeLock wake = getSystemService(PowerManager.class)
                .newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "Sub2API:poll");
        wake.acquire(25_000);
        try {
            String site;
            String token;
            long cursor;
            synchronized (settings.prefs) {
                // 确保更换站点时不会把新站点的口令发送给旧站点。
                site = settings.site();
                token = settings.token();
                cursor = settings.prefs.getLong("cursor", 0);
            }
            long deadline = SystemClock.elapsedRealtime() + 20_000;
            for (int page = 0; page < 5 && generation == currentGeneration && SystemClock.elapsedRealtime() < deadline; page++) {
                JSONArray alerts = fetch(site, token, cursor, deadline);
                for (int i = 0; i < alerts.length(); i++) {
                    JSONObject alert = alerts.getJSONObject(i);
                    long id = alert.getLong("id");
                    if (id <= cursor) continue;
                    String kind = alert.getString("kind");
                    String title = alert.getString("title");
                    String body = alert.getString("body");
                    String channel = "multiplier_changed".equals(kind) ? AlertNotifications.RATE : AlertNotifications.BALANCE;
                    if (!AlertNotifications.canNotify(this, channel)) {
                        throw new IOException("通知权限已关闭");
                    }
                    synchronized (settings.prefs) {
                        if (generation != currentGeneration || !site.equals(settings.site())) return;
                        AlertNotifications.show(this, "alert:" + id, kind, title, body);
                        if (!settings.prefs.edit().putLong("cursor", id).putString("last_alert", title + "\n" + body).commit()) {
                            throw new IOException("进度保存失败");
                        }
                    }
                    cursor = id;
                }
                if (alerts.length() < 100) break;
            }
            if (generation == currentGeneration) {
                settings.prefs.edit().putLong("last_success", System.currentTimeMillis()).putString("status", "已连接，正在检查告警").apply();
            }
        } catch (Exception error) {
            if (generation == currentGeneration) {
                status(error instanceof AlertConnectionException ? error.getMessage() : "暂时无法读取告警，将自动重连");
            }
        } finally {
            if (wake.isHeld()) wake.release();
        }
    }

    private JSONArray fetch(String site, String token, long cursor, long deadline) throws Exception {
        URL endpoint = new URL(site + "/api/v1/mobile-alerts?after=" + cursor);
        HttpsURLConnection request = (HttpsURLConnection) endpoint.openConnection();
        activeRequest = request;
        try {
            request.setInstanceFollowRedirects(false);
            request.setConnectTimeout(5_000);
            request.setReadTimeout(5_000);
            request.setUseCaches(false);
            request.setRequestProperty("Authorization", "Bearer " + token);
            request.setRequestProperty("Accept", "application/json");
            int code = request.getResponseCode();
            if (code == 401) throw new AlertConnectionException("专用口令错误，或服务端尚未启用手机告警");
            if (code == 404) throw new AlertConnectionException("站点尚未部署手机告警接口");
            if (code != 200) throw new IOException("读取失败");
            try (InputStream in = request.getInputStream(); ByteArrayOutputStream bytes = new ByteArrayOutputStream()) {
                byte[] chunk = new byte[4096];
                int size;
                while ((size = in.read(chunk)) != -1) {
                    if (bytes.size() + size > 512 * 1024 || SystemClock.elapsedRealtime() >= deadline) {
                        throw new IOException("响应过大或读取超时");
                    }
                    bytes.write(chunk, 0, size);
                }
                JSONObject envelope = new JSONObject(new String(bytes.toByteArray(), StandardCharsets.UTF_8));
                if (envelope.getInt("code") != 0) throw new IOException("读取失败");
                return envelope.getJSONArray("data");
            }
        } finally {
            request.disconnect();
            activeRequest = null;
        }
    }

    private void status(String message) {
        settings.prefs.edit().putString("status", message).apply();
    }

    @Override public void onDestroy() {
        generation++;
        worker.shutdownNow();
        HttpsURLConnection request = activeRequest;
        if (request != null) request.disconnect();
        stopForeground(STOP_FOREGROUND_REMOVE);
        status("监控已停止");
        super.onDestroy();
    }

    private static final class AlertConnectionException extends IOException {
        AlertConnectionException(String message) {
            super(message);
        }
    }
}
