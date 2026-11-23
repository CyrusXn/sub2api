package eu.cc.xnkaixin.alerts;

import android.app.Service;
import android.content.Intent;
import android.os.Handler;
import android.os.IBinder;
import android.os.Looper;

import org.json.JSONObject;

import java.io.BufferedReader;
import java.io.IOException;
import java.io.InputStreamReader;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.ScheduledThreadPoolExecutor;
import java.util.concurrent.TimeUnit;
import javax.net.ssl.HttpsURLConnection;

public final class AlertService extends Service {
    static final String RECONNECT = "eu.cc.xnkaixin.alerts.RECONNECT";
    private final ScheduledThreadPoolExecutor worker = new ScheduledThreadPoolExecutor(1);
    private final Handler updateTimer = new Handler(Looper.getMainLooper());
    private final Runnable checkUpdate = new Runnable() {
        @Override public void run() {
            AlertUpdater.check(AlertService.this, false);
            updateTimer.postDelayed(this, AlertUpdater.CHECK_INTERVAL);
        }
    };
    private AlertSettings settings;
    private volatile int generation;
    private boolean started;

    @Override public IBinder onBind(Intent intent) {
        return null;
    }

    @Override public void onCreate() {
        super.onCreate();
        settings = new AlertSettings(this);
        AlertNotifications.createChannels(this);
    }

    @Override public int onStartCommand(Intent intent, int flags, int startId) {
        if (!settings.hasToken()) {
            stopSelf();
            return START_NOT_STICKY;
        }
        startForeground(1, AlertNotifications.serviceNotification(this));
        if (!started) updateTimer.post(checkUpdate);
        if (!started || (intent != null && RECONNECT.equals(intent.getAction()))) {
            started = true;
            int current = ++generation;
            worker.execute(() -> receive(current, 2));
        }
        return START_STICKY;
    }

    private void receive(int current, int retrySeconds) {
        if (generation != current) return;
        int delay = retrySeconds;
        try {
            listen(current);
            // 正常换连接后立即补收；短暂异常逐次退避。
            delay = 1;
            retrySeconds = 2;
        } catch (Exception error) {
            status(current, error instanceof AlertConnectionException ? error.getMessage() : "连接中断，正在自动重连");
        }
        int next = Math.min(retrySeconds * 2, 30);
        if (generation == current) worker.schedule(() -> receive(current, next), delay, TimeUnit.SECONDS);
    }

    private void listen(int current) throws Exception {
        String site;
        String token;
        long cursor;
        synchronized (settings.prefs) {
            site = settings.site();
            token = settings.token();
            cursor = settings.prefs.getLong("cursor", 0);
        }
        HttpsURLConnection request = (HttpsURLConnection) new URL(site + "/api/v1/mobile-alerts/stream?after=" + cursor).openConnection();
        try {
            request.setInstanceFollowRedirects(false);
            request.setConnectTimeout(5_000);
            request.setReadTimeout(35_000);
            request.setUseCaches(false);
            request.setRequestProperty("Authorization", "Bearer " + token);
            request.setRequestProperty("Accept", "text/event-stream");
            int code = request.getResponseCode();
            if (code == 401) throw new AlertConnectionException("专用口令错误，或服务端尚未启用手机告警");
            if (code == 404) throw new AlertConnectionException("站点尚未部署手机推送接口");
            if (code != 200 || request.getContentType() == null || !request.getContentType().startsWith("text/event-stream")) {
                throw new IOException("推送连接失败");
            }
            if (generation != current) return;
            settings.prefs.edit().putLong("last_success", System.currentTimeMillis()).putString("status", "已连接，等待服务器推送").apply();
            try (BufferedReader input = new BufferedReader(new InputStreamReader(request.getInputStream(), StandardCharsets.UTF_8))) {
                String pending = null;
                String line;
                while (generation == current && (line = input.readLine()) != null) {
                    if (line.startsWith("data: ")) {
                        pending = line.substring(6);
                    } else if (line.isEmpty() && pending != null) {
                        cursor = deliver(current, site, cursor, new JSONObject(pending));
                        pending = null;
                    }
                }
            }
        } finally {
            request.disconnect();
        }
    }

    private long deliver(int current, String site, long cursor, JSONObject alert) throws Exception {
        long id = alert.getLong("id");
        if (id <= cursor) return cursor;
        String kind = alert.getString("kind");
        String title = alert.getString("title");
        String body = alert.getString("body");
        synchronized (settings.prefs) {
            if (generation != current || !site.equals(settings.site())) return cursor;
            String channel = "multiplier_changed".equals(kind) ? AlertNotifications.RATE : AlertNotifications.BALANCE;
            if (!AlertNotifications.canNotify(this, channel)) throw new AlertConnectionException("请在系统中允许告警通知");
            AlertNotifications.show(this, "alert:" + id, kind, title, body);
            if (!settings.prefs.edit().putLong("cursor", id).putString("last_alert", body.isEmpty() ? title : title + "\n" + body).commit()) {
                throw new IOException("进度保存失败");
            }
        }
        return id;
    }

    private void status(int current, String message) {
        if (generation == current) settings.prefs.edit().putString("status", message).apply();
    }

    @Override public void onDestroy() {
        updateTimer.removeCallbacks(checkUpdate);
        ++generation;
        worker.shutdownNow();
        super.onDestroy();
    }

    private static final class AlertConnectionException extends IOException {
        AlertConnectionException(String message) { super(message); }
    }
}
