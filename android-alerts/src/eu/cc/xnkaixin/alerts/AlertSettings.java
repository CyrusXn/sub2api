package eu.cc.xnkaixin.alerts;

import android.content.Context;
import android.content.SharedPreferences;
import android.security.keystore.KeyGenParameterSpec;
import android.security.keystore.KeyProperties;
import android.util.Base64;

import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.security.GeneralSecurityException;
import java.security.KeyStore;
import javax.crypto.Cipher;
import javax.crypto.KeyGenerator;
import javax.crypto.SecretKey;
import javax.crypto.spec.GCMParameterSpec;

final class AlertSettings {
    static final String DEFAULT_SITE = "https://api.xnkaixin.eu.cc";
    private static final String KEY_ALIAS = "mobile-alert-token";
    final SharedPreferences prefs;

    AlertSettings(Context context) {
        prefs = context.getSharedPreferences("mobile_alerts", Context.MODE_PRIVATE);
    }

    String site() {
        return prefs.getString("site", DEFAULT_SITE);
    }

    boolean hasToken() {
        return prefs.contains("token_ciphertext");
    }

    static String validateSite(String raw) {
        URI uri = URI.create(raw);
        if (!"https".equalsIgnoreCase(uri.getScheme()) || uri.getHost() == null || uri.getUserInfo() != null
                || uri.getRawQuery() != null || uri.getRawFragment() != null
                || !(uri.getRawPath().isEmpty() || "/".equals(uri.getRawPath()))
                || uri.getPort() == 0 || uri.getPort() > 65535) {
            throw new IllegalArgumentException("请输入 HTTPS 站点地址，例如 " + DEFAULT_SITE);
        }
        return "https://" + uri.getRawAuthority();
    }

    void save(String site, String token) throws Exception {
        boolean siteChanged = !site.equals(site());
        if (token.isEmpty() && (siteChanged || !hasToken())) {
            throw new IllegalArgumentException("请输入该站点的手机告警口令");
        }
        SharedPreferences.Editor editor = prefs.edit().putString("site", site);
        if (siteChanged) {
            editor.remove("cursor").remove("last_alert").remove("last_success");
        }
        if (!token.isEmpty()) {
            if (!token.matches("[A-Za-z0-9_-]{32,256}")) {
                throw new IllegalArgumentException("请使用专用手机告警口令");
            }
            Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
            cipher.init(Cipher.ENCRYPT_MODE, secretKey());
            editor.putString("token_iv", encode(cipher.getIV()));
            editor.putString("token_ciphertext", encode(cipher.doFinal(token.getBytes(StandardCharsets.UTF_8))));
        }
        if (!editor.commit()) {
            throw new GeneralSecurityException("保存连接配置失败");
        }
    }

    String token() throws Exception {
        Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
        byte[] iv = Base64.decode(prefs.getString("token_iv", ""), Base64.NO_WRAP);
        byte[] encrypted = Base64.decode(prefs.getString("token_ciphertext", ""), Base64.NO_WRAP);
        cipher.init(Cipher.DECRYPT_MODE, secretKey(), new GCMParameterSpec(128, iv));
        return new String(cipher.doFinal(encrypted), StandardCharsets.UTF_8);
    }

    private SecretKey secretKey() throws Exception {
        KeyStore store = KeyStore.getInstance("AndroidKeyStore");
        store.load(null);
        if (store.getKey(KEY_ALIAS, null) instanceof SecretKey) {
            return (SecretKey) store.getKey(KEY_ALIAS, null);
        }
        KeyGenerator generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore");
        generator.init(new KeyGenParameterSpec.Builder(KEY_ALIAS, KeyProperties.PURPOSE_ENCRYPT | KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .build());
        return generator.generateKey();
    }

    private static String encode(byte[] data) {
        return Base64.encodeToString(data, Base64.NO_WRAP);
    }
}
