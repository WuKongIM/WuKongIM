/*
 * Copyright (C) 2018 Red Hat, Inc.
 *
 * Licensed under the GNU Lesser General Public License Version 2.1
 *
 * This library is free software; you can redistribute it and/or
 * modify it under the terms of the GNU Lesser General Public
 * License as published by the Free Software Foundation; either
 * version 2.1 of the License, or (at your option) any later version.
 *
 * This library is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the GNU
 * Lesser General Public License for more details.
 *
 * You should have received a copy of the GNU Lesser General Public
 * License along with this library; if not, write to the Free Software
 * Foundation, Inc., 51 Franklin Street, Fifth Floor, Boston, MA 02110-1301 USA
 */

#define METADATA_RELATIVE_DIR "repodata"
#define PACKAGES_RELATIVE_DIR "packages"
#define METALINK_FILENAME "metalink.xml"
#define MIRRORLIST_FILENAME  "mirrorlist"
#define RECOGNIZED_CHKSUMS {"sha512", "sha256"}

#include "../log.hpp"
#include "Repo-private.hpp"
#include "../dnf-utils.h"
#include "../dnf-context.hpp"
#include "../hy-iutil.h"
#include "../hy-repo-private.hpp"
#include "../hy-util-private.hpp"
#include "../hy-iutil-private.hpp"
#include "../hy-types.h"
#include "libdnf/utils/File.hpp"
#include "libdnf/utils/utils.hpp"
#include "libdnf/utils/os-release.hpp"
#include "libdnf/utils/url-encode.hpp"

#include "bgettext/bgettext-lib.h"
#include "tinyformat/tinyformat.hpp"
#include <utils.hpp>

#include <librepo/librepo.h>
#include <fcntl.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <unistd.h>
#include <utime.h>

#include <gpgme.h>

#include <solv/chksum.h>
#include <solv/repo.h>
#include <solv/util.h>

#include <array>
#include <atomic>
#include <cctype>
#include <cerrno>
#include <fstream>
#include <iomanip>
#include <iostream>
#include <list>
#include <map>
#include <set>
#include <sstream>
#include <system_error>
#include <type_traits>

#include <stdio.h>
#include <string.h>
#include <time.h>

#include <glib.h>

//
// COUNTME CONSTANTS
//
// width of the sliding time window (in seconds)
const int COUNTME_WINDOW = 7*24*60*60;  // 1 week
// starting point of the sliding time window relative to the UNIX epoch
// allows for aligning the window with a specific weekday
const int COUNTME_OFFSET = 345600;  // Monday (1970-01-05 00:00:00 UTC)
// estimated number of metalink requests sent over the window
// used to generate the probability distribution of counting events
const int COUNTME_BUDGET = 4;  // metadata_expire defaults to 2 days
// cookie file name
const std::string COUNTME_COOKIE = "countme";
// cookie file format version
const int COUNTME_VERSION = 0;
// longevity buckets that we report in the flag
// example: {A, B, C} defines 4 buckets [0, A), [A, B), [B, C), [C, infinity)
// where each letter represents a window step (starting from 0)
const std::array<const int, 3> COUNTME_BUCKETS = { {2, 5, 25} };

namespace std {

template<>
struct default_delete<GError> {
    void operator()(GError * ptr) noexcept { g_error_free(ptr); }
};

template<>
struct default_delete<LrResult> {
    void operator()(LrResult * ptr) noexcept { lr_result_free(ptr); }
};

template<>
struct default_delete<LrPackageTarget> {
    void operator()(LrPackageTarget * ptr) noexcept { lr_packagetarget_free(ptr); }
};

template<>
struct default_delete<std::remove_pointer<gpgme_ctx_t>::type> {
    void operator()(gpgme_ctx_t ptr) noexcept { gpgme_release(ptr); }
};

} // namespace std

namespace libdnf {

class LrExceptionWithSourceUrl : public LrException {
public:
    LrExceptionWithSourceUrl(int code, const std::string & msg, const std::string & sourceUrl)
        : LrException(code, msg), sourceUrl(sourceUrl) {}
    const std::string & getSourceUrl() const { return sourceUrl; }
private:
    std::string sourceUrl;
};

static void throwException(std::unique_ptr<GError> && err)
{
    throw LrException(err->code, err->message);
}

template<typename T>
inline static void handleSetOpt(LrHandle * handle, LrHandleOption option, T value)
{
    GError * errP{nullptr};
    if (!lr_handle_setopt(handle, &errP, option, value)) {
        throwException(std::unique_ptr<GError>(errP));
    }
}

inline static void handleGetInfo(LrHandle * handle, LrHandleInfoOption option, void * value)
{
    GError * errP{nullptr};
    if (!lr_handle_getinfo(handle, &errP, option, value)) {
        throwException(std::unique_ptr<GError>(errP));
    }
}

template<typename T>
inline static void resultGetInfo(LrResult * result, LrResultInfoOption option, T value)
{
    GError * errP{nullptr};
    if (!lr_result_getinfo(result, &errP, option, value)) {
        throwException(std::unique_ptr<GError>(errP));
    }
}

/* Callback stuff */

int RepoCB::progress(double totalToDownload, double downloaded) { return 0; }
void RepoCB::fastestMirror(FastestMirrorStage stage, const char * ptr) {}
int RepoCB::handleMirrorFailure(const char * msg, const char * url, const char * metadata) { return 0; }

bool RepoCB::repokeyImport(const std::string & id, const std::string & userId,
                           const std::string & fingerprint, const std::string & url, long int timestamp)
{
    return true;
}


// Map string from config option proxy_auth_method to librepo LrAuth value
static constexpr struct {
    const char * name;
    LrAuth code;
} PROXYAUTHMETHODS[] = {
    {"none", LR_AUTH_NONE},
    {"basic", LR_AUTH_BASIC},
    {"digest", LR_AUTH_DIGEST},
    {"negotiate", LR_AUTH_NEGOTIATE},
    {"ntlm", LR_AUTH_NTLM},
    {"digest_ie", LR_AUTH_DIGEST_IE},
    {"ntlm_wb", LR_AUTH_NTLM_WB},
    {"any", LR_AUTH_ANY}
};

bool Repo::Impl::endsWith(const std::string &str, const std::string &ending) const {
    if (str.length() >= ending.length())
        return (str.compare(str.length() - ending.length(), ending.length(), ending) == 0);
    else
        return false;
}

const std::string & Repo::Impl::getMetadataPath(const std::string &metadataType) const {
//    auto logger(Log::getLogger());
    static const std::string empty;
    std::string lookupMetadataType = metadataType;
    if (conf->getMainConfig().zchunk().getValue()) {
        if(!endsWith(metadataType, "_zck"))
            lookupMetadataType = metadataType + "_zck";
    }
    auto it = metadataPaths.find(lookupMetadataType);
    if(it == metadataPaths.end() && lookupMetadataType != metadataType)
        it = metadataPaths.find(metadataType);
    auto & ret = (it != metadataPaths.end()) ? it->second : empty;
//    if (ret.empty())
//        logger->debug(tfm::format("not found \"%s\" for: %s", metadataType, conf->name().getValue()));
    return ret;
}

int Repo::Impl::progressCB(void * data, double totalToDownload, double downloaded)
{
    if (!data)
        return 0;
    auto cbObject = static_cast<RepoCB *>(data);
    return cbObject->progress(totalToDownload, downloaded);
}

void Repo::Impl::fastestMirrorCB(void * data, LrFastestMirrorStages stage, void *ptr)
{
    if (!data)
        return;
    auto cbObject = static_cast<RepoCB *>(data);
    const char * msg;
    std::string msgString;
    if (ptr) {
        switch (stage) {
            case LR_FMSTAGE_CACHELOADING:
            case LR_FMSTAGE_CACHELOADINGSTATUS:
            case LR_FMSTAGE_STATUS:
                msg = static_cast<const char *>(ptr);
                break;
            case LR_FMSTAGE_DETECTION:
                msgString = std::to_string(*((long *)ptr));
                msg = msgString.c_str();
                break;
            default:
                msg = nullptr;
        }
    } else
        msg = nullptr;
    cbObject->fastestMirror(static_cast<RepoCB::FastestMirrorStage>(stage), msg);
}

int Repo::Impl::mirrorFailureCB(void * data, const char * msg, const char * url, const char * metadata)
{
    if (!data)
        return 0;
    auto cbObject = static_cast<RepoCB *>(data);
    return cbObject->handleMirrorFailure(msg, url, metadata);
};


/**
* @brief Format user password string
*
* Returns user and password in user:password form. If quote is True,
* special characters in user and password are URL encoded.
*
* @param user Username
* @param passwd Password
* @param encode If quote is True, special characters in user and password are URL encoded.
* @return User and password in user:password form
*/
static std::string formatUserPassString(const std::string & user, const std::string & passwd, bool encode)
{
    if (encode)
        return urlEncode(user) + ":" + urlEncode(passwd);
    else
        return user + ":" + passwd;
}

Repo::Impl::Impl(Repo & owner, const std::string & id, Type type, std::unique_ptr<ConfigRepo> && conf)
: id(id), type(type), conf(std::move(conf)), timestamp(-1), loadMetadataOther(false)
, syncStrategy(SyncStrategy::TRY_CACHE), owner(&owner), expired(false) {}

Repo::Impl::~Impl()
{
    g_strfreev(mirrors);
    if (libsolvRepo)
        libsolvRepo->appdata = nullptr;
}

Repo::Repo(const std::string & id, std::unique_ptr<ConfigRepo> && conf, Repo::Type type)
{
    if (type == Type::AVAILABLE) {
        auto idx = verifyId(id);
        if (idx >= 0) {
            std::string msg = tfm::format(
                ("Invalid repository id \"%s\": invalid character '%s' at position %d."),
                id, id[idx], idx + 1);
            throw RepoError(msg);
        }
    }
    pImpl.reset(new Impl(*this, id, type, std::move(conf)));
}

Repo::~Repo() = default;

void Repo::setCallbacks(std::unique_ptr<RepoCB> && callbacks)
{
    pImpl->callbacks = std::move(callbacks);
}

int Repo::verifyId(const std::string & id)
{
    auto idx = id.find_first_not_of(REPOID_CHARS);
    return idx == id.npos ? -1 : idx;
}

void Repo::verify() const
{
    if (pImpl->conf->baseurl().empty() &&
        (pImpl->conf->metalink().empty() || pImpl->conf->metalink().getValue().empty()) &&
        (pImpl->conf->mirrorlist().empty() || pImpl->conf->mirrorlist().getValue().empty()))
        throw RepoError(tfm::format(_("Repository %s has no mirror or baseurl set."), pImpl->id));

    const auto & type = pImpl->conf->type().getValue();
    const char * supportedRepoTypes[]{"rpm-md", "rpm", "repomd", "rpmmd", "yum", "YUM"};
    if (!type.empty()) {
        for (auto supported : supportedRepoTypes) {
            if (type == supported)
                return;
        }
        throw RepoError(tfm::format(_("Repository '%s' has unsupported type: 'type=%s', skipping."),
                                      pImpl->id, type));
    }
}

ConfigRepo * Repo::getConfig() noexcept
{
    return pImpl->conf.get();
}

const std::string & Repo::getId() const noexcept
{
    return pImpl->id;
}

void Repo::enable()
{
    pImpl->conf->enabled().set(Option::Priority::RUNTIME, true);
}

void Repo::disable()
{
    pImpl->conf->enabled().set(Option::Priority::RUNTIME, false);
}

bool Repo::isEnabled() const
{
    return pImpl->conf->enabled().getValue();
}

bool Repo::isLocal() const
{
    auto & conf = pImpl->conf;
    if ((!conf->metalink().empty() && !conf->metalink().getValue().empty()) ||
        (!conf->mirrorlist().empty() && !conf->mirrorlist().getValue().empty()))
        return false;
    if (!conf->baseurl().getValue().empty() && conf->baseurl().getValue()[0].compare(0, 7, "file://") == 0)
        return true;
    return false;
}

std::string Repo::getLocalBaseurl() const
{
    if (!isLocal()) {
        throw Exception("Invalid call getLocalBaseurl() on a non-local repository.");
    }

    // isLocal() already ensured the first item in the list starts with "file://"
    return urlDecode(pImpl->conf->baseurl().getValue()[0].substr(7));
}

bool Repo::load() { return pImpl->load(); }
bool Repo::loadCache(bool throwExcept, bool ignoreMissing) { return pImpl->loadCache(throwExcept, ignoreMissing); }
void Repo::downloadMetadata(const std::string & destdir) { pImpl->downloadMetadata(destdir); }
bool Repo::getUseIncludes() const { return pImpl->useIncludes; }
void Repo::setUseIncludes(bool enabled) { pImpl->useIncludes = enabled; }
bool Repo::getLoadMetadataOther() const { return pImpl->loadMetadataOther; }
void Repo::setLoadMetadataOther(bool value) { pImpl->loadMetadataOther = value; }
int Repo::getCost() const { return pImpl->conf->cost().getValue(); }
int Repo::getPriority() const { return pImpl->conf->priority().getValue(); }
std::string Repo::getCompsFn() {
    auto tmp = pImpl->getMetadataPath(MD_TYPE_GROUP_GZ);
    if (tmp.empty())
        tmp = pImpl->getMetadataPath(MD_TYPE_GROUP);
    return tmp;
}


#ifdef MODULEMD
std::string Repo::getModulesFn() { return pImpl->getMetadataPath(MD_TYPE_MODULES); }
#endif

int Repo::getAge() const { return pImpl->getAge(); }
void Repo::expire() { pImpl->expire(); }
bool Repo::isExpired() const { return pImpl->isExpired(); }
int Repo::getExpiresIn() const { return pImpl->getExpiresIn(); }

void Repo::setSubstitutions(const std::map<std::string, std::string> & substitutions)
{
    pImpl->substitutions = substitutions;
}

void Repo::addMetadataTypeToDownload(const std::string &metadataType)
{
    pImpl->additionalMetadata.insert(metadataType);
}

void Repo::removeMetadataTypeFromDownload(const std::string &metadataType)
{
    pImpl->additionalMetadata.erase(metadataType);
}

std::string Repo::getMetadataPath(const std::string &metadataType)
{
    return pImpl->getMetadataPath(metadataType);
}

std::string Repo::getMetadataContent(const std::string &metadataType)
{
    auto path = getMetadataPath(metadataType);
    if (path.empty()) return "";

    auto mdfile = File::newFile(path);
    mdfile->open("r");
    const auto &content = mdfile->getContent();
    mdfile->close();
    return content;
}

std::unique_ptr<LrHandle> Repo::Impl::lrHandleInitBase()
{
    std::unique_ptr<LrHandle> h(lr_handle_init());
    std::vector<const char *> dlist = {MD_TYPE_PRIMARY, MD_TYPE_FILELISTS, MD_TYPE_PRESTODELTA,
        MD_TYPE_GROUP_GZ, MD_TYPE_UPDATEINFO};

#ifdef MODULEMD
    dlist.push_back(MD_TYPE_MODULES);
#endif
    if (loadMetadataOther) {
        dlist.push_back(MD_TYPE_OTHER);
    }
    for (auto &item : additionalMetadata) {
        dlist.push_back(item.c_str());
    }
    dlist.push_back(NULL);
    handleSetOpt(h.get(), LRO_PRESERVETIME, static_cast<long>(preserveRemoteTime));
    handleSetOpt(h.get(), LRO_REPOTYPE, LR_YUMREPO);
    handleSetOpt(h.get(), LRO_USERAGENT, conf->user_agent().getValue().c_str());
    handleSetOpt(h.get(), LRO_YUMDLIST, dlist.data());
    handleSetOpt(h.get(), LRO_INTERRUPTIBLE, 1L);
    handleSetOpt(h.get(), LRO_GPGCHECK, conf->repo_gpgcheck().getValue());
    handleSetOpt(h.get(), LRO_MAXMIRRORTRIES, static_cast<long>(maxMirrorTries));
    handleSetOpt(h.get(), LRO_MAXPARALLELDOWNLOADS,
                     conf->max_parallel_downloads().getValue());
    handleSetOpt(h.get(), LRO_MAXDOWNLOADSPERMIRROR,
                     conf->max_downloads_per_mirror().getValue());

    LrUrlVars * vars = NULL;
    vars = lr_urlvars_set(vars, MD_TYPE_GROUP_GZ, MD_TYPE_GROUP);
    handleSetOpt(h.get(), LRO_YUMSLIST, vars);

    return h;
}

std::unique_ptr<LrHandle> Repo::Impl::lrHandleInitLocal()
{
    std::unique_ptr<LrHandle> h(lrHandleInitBase());

    LrUrlVars * vars = NULL;
    for (const auto & item : substitutions)
        vars = lr_urlvars_set(vars, item.first.c_str(), item.second.c_str());
    handleSetOpt(h.get(), LRO_VARSUB, vars);
    auto cachedir = getCachedir();
    handleSetOpt(h.get(), LRO_DESTDIR, cachedir.c_str());
    const char *urls[] = {cachedir.c_str(), NULL};
    handleSetOpt(h.get(), LRO_URLS, urls);
    handleSetOpt(h.get(), LRO_LOCAL, 1L);
#ifdef LRO_SUPPORTS_CACHEDIR
    /* If zchunk is enabled, set librepo cache dir */
    if (conf->getMainConfig().zchunk().getValue()) {
        if (conf->basecachedir().empty()) {
            throw Exception(tfm::format(_("repo '%s': 'basecachedir' is not set"), id));
        }
        handleSetOpt(h.get(), LRO_CACHEDIR, conf->basecachedir().getValue().c_str());
    }
#endif
    return h;
}

template<typename ConfigT>
static void setHandle(LrHandle * h, ConfigT & config, const char * repoId = nullptr) {
    auto & ipResolve = config.ip_resolve().getValue();
    if (ipResolve == "ipv4")
        handleSetOpt(h, LRO_IPRESOLVE, LR_IPRESOLVE_V4);
    else if (ipResolve == "ipv6")
        handleSetOpt(h, LRO_IPRESOLVE, LR_IPRESOLVE_V6);

    auto minrate = config.minrate().getValue();
    handleSetOpt(h, LRO_LOWSPEEDLIMIT, static_cast<long>(minrate));

    auto maxspeed = config.throttle().getValue();
    if (maxspeed > 0 && maxspeed <= 1)
        maxspeed *= config.bandwidth().getValue();
    if (maxspeed != 0 && maxspeed < minrate)
        throw RepoError(_("Maximum download speed is lower than minimum. "
                          "Please change configuration of minrate or throttle"));
    handleSetOpt(h, LRO_MAXSPEED, static_cast<int64_t>(maxspeed));

    long timeout = config.timeout().getValue();
    if (timeout > 0) {
        handleSetOpt(h, LRO_CONNECTTIMEOUT, timeout);
        handleSetOpt(h, LRO_LOWSPEEDTIME, timeout);
    } else {
        handleSetOpt(h, LRO_CONNECTTIMEOUT, LRO_CONNECTTIMEOUT_DEFAULT);
        handleSetOpt(h, LRO_LOWSPEEDTIME, LRO_LOWSPEEDTIME_DEFAULT);
    }

    // setup username/password if needed
    auto userpwd = config.username().getValue();
    if (!userpwd.empty()) {
        // TODO Use URL encoded form, needs support in librepo
        userpwd = formatUserPassString(userpwd, config.password().getValue(), false);
        handleSetOpt(h, LRO_USERPWD, userpwd.c_str());
    }

    if (!config.proxy().empty() && !config.proxy().getValue().empty())
        handleSetOpt(h, LRO_PROXY, config.proxy().getValue().c_str());

    //set proxy authorization method
    auto proxyAuthMethods = Repo::Impl::stringToProxyAuthMethods(config.proxy_auth_method().getValue());
    handleSetOpt(h, LRO_PROXYAUTHMETHODS, static_cast<long>(proxyAuthMethods));

    // setup proxy username/password if needed
    if (!config.proxy_username().empty()) {
        auto userpwd = config.proxy_username().getValue();
        if (!userpwd.empty()) {
            if (config.proxy_password().empty()) {
                if (repoId)
                    throw RepoError(tfm::format(_("repo '%s': 'proxy_username' is set but not 'proxy_password'"), repoId));
                else
                    throw RepoError(_("'proxy_username' is set but not 'proxy_password'"));
            }
            userpwd = formatUserPassString(userpwd, config.proxy_password().getValue(), true);
            handleSetOpt(h, LRO_PROXYUSERPWD, userpwd.c_str());
        }
    }

    // setup ssl stuff
    if (!config.sslcacert().getValue().empty())
        handleSetOpt(h, LRO_SSLCACERT, config.sslcacert().getValue().c_str());
    if (!config.sslclientcert().getValue().empty())
        handleSetOpt(h, LRO_SSLCLIENTCERT, config.sslclientcert().getValue().c_str());
    if (!config.sslclientkey().getValue().empty())
        handleSetOpt(h, LRO_SSLCLIENTKEY, config.sslclientkey().getValue().c_str());
    long sslverify = config.sslverify().getValue() ? 1L : 0L;
    handleSetOpt(h, LRO_SSLVERIFYHOST, sslverify);
    handleSetOpt(h, LRO_SSLVERIFYPEER, sslverify);
    handleSetOpt(h, LRO_SSLVERIFYSTATUS, config.sslverifystatus().getValue() ? 1L : 0L);

    // setup proxy ssl stuff
    if (!config.proxy_sslcacert().getValue().empty())
        handleSetOpt(h, LRO_PROXY_SSLCACERT, config.proxy_sslcacert().getValue().c_str());
    if (!config.proxy_sslclientcert().getValue().empty())
        handleSetOpt(h, LRO_PROXY_SSLCLIENTCERT, config.proxy_sslclientcert().getValue().c_str());
    if (!config.proxy_sslclientkey().getValue().empty())
        handleSetOpt(h, LRO_PROXY_SSLCLIENTKEY, config.proxy_sslclientkey().getValue().c_str());
    long proxy_sslverify = config.proxy_sslverify().getValue() ? 1L : 0L;
    handleSetOpt(h, LRO_PROXY_SSLVERIFYHOST, proxy_sslverify);
    handleSetOpt(h, LRO_PROXY_SSLVERIFYPEER, proxy_sslverify);
}

std::unique_ptr<LrHandle> Repo::Impl::lrHandleInitRemote(const char *destdir)
{
    std::unique_ptr<LrHandle> h(lrHandleInitBase());
    handleSetOpt(h.get(), LRO_HTTPHEADER, httpHeaders.get());

    LrUrlVars * vars = NULL;
    for (const auto & item : substitutions)
        vars = lr_urlvars_set(vars, item.first.c_str(), item.second.c_str());
    handleSetOpt(h.get(), LRO_VARSUB, vars);

    handleSetOpt(h.get(), LRO_DESTDIR, destdir);

    enum class Source {NONE, METALINK, MIRRORLIST} source{Source::NONE};
    std::string tmp;
    if (!conf->metalink().empty() && !(tmp=conf->metalink().getValue()).empty())
        source = Source::METALINK;
    else if (!conf->mirrorlist().empty() && !(tmp=conf->mirrorlist().getValue()).empty())
        source = Source::MIRRORLIST;
    if (source != Source::NONE) {
        handleSetOpt(h.get(), LRO_PROGRESSDATA, callbacks.get());
        if (source == Source::METALINK)
            handleSetOpt(h.get(), LRO_METALINKURL, tmp.c_str());
        else {
            handleSetOpt(h.get(), LRO_MIRRORLISTURL, tmp.c_str());
            // YUM-DNF compatibility hack. YUM guessed by content of keyword "metalink" if
            // mirrorlist is really mirrorlist or metalink)
            if (tmp.find("metalink") != tmp.npos)
                handleSetOpt(h.get(), LRO_METALINKURL, tmp.c_str());
        }
        handleSetOpt(h.get(), LRO_FASTESTMIRROR, conf->fastestmirror().getValue() ? 1L : 0L);
        if (conf->basecachedir().empty()) {
            throw Exception(tfm::format(_("repo '%s': 'basecachedir' is not set"), id));
        }
        auto fastestMirrorCacheDir = conf->basecachedir().getValue();
        if (fastestMirrorCacheDir.back() != '/')
            fastestMirrorCacheDir.push_back('/');
        fastestMirrorCacheDir += "fastestmirror.cache";
        handleSetOpt(h.get(), LRO_FASTESTMIRRORCACHE, fastestMirrorCacheDir.c_str());
    }

    if (!conf->baseurl().getValue().empty()) {
        size_t len = conf->baseurl().getValue().size();
        const char * urls[len + 1];
        for (size_t idx = 0; idx < len; ++idx)
            urls[idx] = conf->baseurl().getValue()[idx].c_str();
        urls[len] = nullptr;
        handleSetOpt(h.get(), LRO_URLS, urls);
    }

    if (source == Source::NONE && conf->baseurl().getValue().empty())
        throw RepoError(tfm::format(_("Cannot find a valid baseurl for repo: %s"), id));

    handleSetOpt(h.get(), LRO_HMFCB, static_cast<LrHandleMirrorFailureCb>(mirrorFailureCB));
    handleSetOpt(h.get(), LRO_PROGRESSCB, static_cast<LrProgressCb>(progressCB));
    handleSetOpt(h.get(), LRO_PROGRESSDATA, callbacks.get());
    handleSetOpt(h.get(), LRO_FASTESTMIRRORCB, static_cast<LrFastestMirrorCb>(fastestMirrorCB));
    handleSetOpt(h.get(), LRO_FASTESTMIRRORDATA, callbacks.get());

#ifdef LRO_SUPPORTS_CACHEDIR
    /* If zchunk is enabled, set librepo cache dir */
    if (conf->getMainConfig().zchunk().getValue()) {
        if (conf->basecachedir().empty()) {
            throw Exception(tfm::format(_("repo '%s': 'basecachedir' is not set"), id));
        }
        handleSetOpt(h.get(), LRO_CACHEDIR, conf->basecachedir().getValue().c_str());
    }
#endif

    setHandle(h.get(), *conf, id.c_str());

    return h;
}

static void gpgImportKey(gpgme_ctx_t context, int keyFd)
{
    auto logger(Log::getLogger());
    gpg_error_t gpgErr;
    gpgme_data_t keyData;

    gpgErr = gpgme_data_new_from_fd(&keyData, keyFd);
    if (gpgErr != GPG_ERR_NO_ERROR) {
        auto msg = tfm::format(_("%s: gpgme_data_new_from_fd(): %s"), __func__, gpgme_strerror(gpgErr));
        logger->debug(msg);
        throw LrException(LRE_GPGERROR, msg);
    }

    gpgErr = gpgme_op_import(context, keyData);
    gpgme_data_release(keyData);
    if (gpgErr != GPG_ERR_NO_ERROR) {
        auto msg = tfm::format(_("%s: gpgme_op_import(): %s"), __func__, gpgme_strerror(gpgErr));
        logger->debug(msg);
        throw LrException(LRE_GPGERROR, msg);
    }
}

static void gpgImportKey(gpgme_ctx_t context, std::vector<char> key)
{
    auto logger(Log::getLogger());
    gpg_error_t gpgErr;
    gpgme_data_t keyData;

    gpgErr = gpgme_data_new_from_mem(&keyData, key.data(), key.size(), 0);
    if (gpgErr != GPG_ERR_NO_ERROR) {
        auto msg = tfm::format(_("%s: gpgme_data_new_from_fd(): %s"), __func__, gpgme_strerror(gpgErr));
        logger->debug(msg);
        throw LrException(LRE_GPGERROR, msg);
    }

    gpgErr = gpgme_op_import(context, keyData);
    gpgme_data_release(keyData);
    if (gpgErr != GPG_ERR_NO_ERROR) {
        auto msg = tfm::format(_("%s: gpgme_op_import(): %s"), __func__, gpgme_strerror(gpgErr));
        logger->debug(msg);
        throw LrException(LRE_GPGERROR, msg);
    }
}

static std::vector<Key> rawkey2infos(int fd) {
    auto logger(Log::getLogger());
    gpg_error_t gpgErr;

    std::vector<Key> keyInfos;
    gpgme_ctx_t ctx;
    gpgme_new(&ctx);
    std::unique_ptr<std::remove_pointer<gpgme_ctx_t>::type> context(ctx);

    // set GPG home dir
    char tmpdir[] = "/tmp/tmpdir.XXXXXX";
    if (!mkdtemp(tmpdir)) {
        const char * errTxt = strerror(errno);
        throw RepoError(tfm::format(_("Cannot create repo temporary directory \"%s\": %s"),
                                      tmpdir, errTxt));
    }
    Finalizer tmpDirRemover([&tmpdir](){
        dnf_remove_recursive(tmpdir, NULL);
    });
    gpgErr = gpgme_ctx_set_engine_info(ctx, GPGME_PROTOCOL_OpenPGP, NULL, tmpdir);
    if (gpgErr != GPG_ERR_NO_ERROR) {
        auto msg = tfm::format(_("%s: gpgme_ctx_set_engine_info(): %s"), __func__, gpgme_strerror(gpgErr));
        logger->debug(msg);
        throw LrException(LRE_GPGERROR, msg);
    }

    gpgImportKey(ctx, fd);

    gpgme_key_t key;
    gpgErr = gpgme_op_keylist_start(ctx, NULL, 0);
    while (!gpgErr)
    {
        gpgErr = gpgme_op_keylist_next(ctx, &key);
        if (gpgErr)
            break;

        // _extract_signing_subkey
        auto subkey = key->subkeys;
        while (subkey && !subkey->can_sign) {
            subkey = subkey->next;
        }
        if (subkey)
            keyInfos.emplace_back(key, subkey);
        gpgme_key_release(key);
    }
    if (gpg_err_code(gpgErr) != GPG_ERR_EOF)
    {
        gpgme_op_keylist_end(ctx);
        auto msg = tfm::format(_("can not list keys: %s"), gpgme_strerror(gpgErr));
        logger->debug(msg);
        throw LrException(LRE_GPGERROR, msg);
    }
    gpgme_set_armor(ctx, 1);
    for (auto & keyInfo : keyInfos) {
        gpgme_data_t sink;
        gpgme_data_new(&sink);
        gpgme_op_export(ctx, keyInfo.getId().c_str(), 0, sink);
        gpgme_data_rewind(sink);

        char buf[4096];
        ssize_t readed;
        do {
            readed = gpgme_data_read(sink, buf, sizeof(buf));
            if (readed > 0)
                keyInfo.rawKey.insert(keyInfo.rawKey.end(), buf, buf + sizeof(buf));
        } while (readed == sizeof(buf));
    }
    return keyInfos;
}

static std::vector<std::string> keyidsFromPubring(const std::string & gpgDir)
{
    auto logger(Log::getLogger());
    gpg_error_t gpgErr;

    std::vector<std::string> keyids;

    struct stat sb;
    if (stat(gpgDir.c_str(), &sb) == 0 && S_ISDIR(sb.st_mode)) {

        gpgme_ctx_t ctx;
        gpgme_new(&ctx);
        std::unique_ptr<std::remove_pointer<gpgme_ctx_t>::type> context(ctx);

        // set GPG home dir
        gpgErr = gpgme_ctx_set_engine_info(ctx, GPGME_PROTOCOL_OpenPGP, NULL, gpgDir.c_str());
        if (gpgErr != GPG_ERR_NO_ERROR) {
            auto msg = tfm::format(_("%s: gpgme_ctx_set_engine_info(): %s"), __func__, gpgme_strerror(gpgErr));
            logger->debug(msg);
            throw LrException(LRE_GPGERROR, msg);
        }

        gpgme_key_t key;
        gpgErr = gpgme_op_keylist_start(ctx, NULL, 0);
        while (!gpgErr)
        {
            gpgErr = gpgme_op_keylist_next(ctx, &key);
            if (gpgErr)
                break;

            // _extract_signing_subkey
            auto subkey = key->subkeys;
            while (subkey && !key->subkeys->can_sign) {
                subkey = subkey->next;
            }
            if (subkey)
                keyids.push_back(subkey->keyid);
            gpgme_key_release(key);
        }
        if (gpg_err_code(gpgErr) != GPG_ERR_EOF)
        {
            gpgme_op_keylist_end(ctx);
            auto msg = tfm::format(_("can not list keys: %s"), gpgme_strerror(gpgErr));
            logger->debug(msg);
            throw LrException(LRE_GPGERROR, msg);
        }
    }
    return keyids;
}

// download key from URL
std::vector<Key> Repo::Impl::retrieve(const std::string & url)
{
    auto logger(Log::getLogger());
    char tmpKeyFile[] = "/tmp/repokey.XXXXXX";
    auto fd = mkstemp(tmpKeyFile);
    if (fd == -1) {
        auto msg = tfm::format("Error creating temporary file \"%s\": %s",
            tmpKeyFile, std::system_category().message(errno));
        logger->debug(msg);
        throw LrException(LRE_GPGERROR, msg);
    }
    unlink(tmpKeyFile);
    Finalizer tmpFileCloser([fd](){
        close(fd);
    });

    try {
        downloadUrl(url.c_str(), fd);
    }
    catch (const LrExceptionWithSourceUrl & e) {
        auto msg = tfm::format(_("Failed to retrieve GPG key for repo '%s': %s"), id, e.what());
        throw RepoError(msg);
    }
    lseek(fd, SEEK_SET, 0);
    auto keyInfos = rawkey2infos(fd);
    for (auto & key : keyInfos)
        key.url = url;
    return keyInfos;
}

/*
 * Creates the '/run/user/$UID' directory if it doesn't exist. If this
 * directory exists, gpgagent will create its sockets under
 * '/run/user/$UID/gnupg'.
 *
 * If this directory doesn't exist, gpgagent will create its sockets in gpg
 * home directory, which is under '/var/cache/yum/metadata/' and this was
 * causing trouble with container images, see [1].
 *
 * Previous solution was to send the agent a "KILLAGENT" message, but that
 * would cause a race condition with calling gpgme_release(), see [2], [3],
 * [4].
 *
 * Since the agent doesn't clean up its sockets properly, by creating this
 * directory we make sure they are in a place that is not causing trouble with
 * container images.
 *
 * [1] https://bugzilla.redhat.com/show_bug.cgi?id=1650266
 * [2] https://bugzilla.redhat.com/show_bug.cgi?id=1769831
 * [3] https://github.com/rpm-software-management/microdnf/issues/50
 * [4] https://bugzilla.redhat.com/show_bug.cgi?id=1781601
 */
static void ensure_socket_dir_exists() {
    auto logger(Log::getLogger());
    std::string dirname = "/run/user/" + std::to_string(getuid());
    int res = mkdir(dirname.c_str(), 0700);
    if (res != 0 && errno != EEXIST) {
        logger->debug(tfm::format("Failed to create directory \"%s\": %d - %s",
                                  dirname, errno, strerror(errno)));
    }
}

void Repo::Impl::importRepoKeys()
{
    auto logger(Log::getLogger());

    auto gpgDir = getCachedir() + "/pubring";
    auto knownKeys = keyidsFromPubring(gpgDir);
    ensure_socket_dir_exists();
    for (const auto & gpgkeyUrl : conf->gpgkey().getValue()) {
        auto keyInfos = retrieve(gpgkeyUrl);
        for (auto & keyInfo : keyInfos) {
            if (std::find(knownKeys.begin(), knownKeys.end(), keyInfo.getId()) != knownKeys.end()) {
                logger->debug(tfm::format(_("repo %s: 0x%s already imported"), id, keyInfo.getId()));
                continue;
            }

            if (callbacks) {
                if (!callbacks->repokeyImport(keyInfo.getId(), keyInfo.getUserId(), keyInfo.getFingerprint(),
                                              keyInfo.url, keyInfo.getTimestamp()))
                    continue;
            }

            struct stat sb;
            if (stat(gpgDir.c_str(), &sb) != 0 || !S_ISDIR(sb.st_mode)) {
                int res = mkdir(gpgDir.c_str(), 0777);
                if (res != 0 && errno != EEXIST) {
                    auto msg = tfm::format(_("Failed to create directory \"%s\": %d - %s"),
                                           gpgDir, errno, strerror(errno));
                    throw RepoError(msg);
                }
            }

            gpgme_ctx_t ctx;
            gpgme_new(&ctx);
            std::unique_ptr<std::remove_pointer<gpgme_ctx_t>::type> context(ctx);

            // set GPG home dir
            auto gpgErr = gpgme_ctx_set_engine_info(ctx, GPGME_PROTOCOL_OpenPGP, NULL, gpgDir.c_str());
            if (gpgErr != GPG_ERR_NO_ERROR) {
                auto msg = tfm::format(_("%s: gpgme_ctx_set_engine_info(): %s"), __func__, gpgme_strerror(gpgErr));
                logger->debug(msg);
                throw LrException(LRE_GPGERROR, msg);
            }

            gpgImportKey(ctx, keyInfo.rawKey);

            logger->debug(tfm::format(_("repo %s: imported key 0x%s."), id, keyInfo.getId()));
        }

    }
}

std::unique_ptr<LrResult> Repo::Impl::lrHandlePerform(LrHandle * handle, const std::string & destDirectory,
    bool setGPGHomeDir)
{
    if (setGPGHomeDir) {
        auto pubringdir = getCachedir() + "/pubring";
        handleSetOpt(handle, LRO_GNUPGHOMEDIR, pubringdir.c_str());
    }

    // Start and end is called only if progress callback is set in handle.
    LrProgressCb progressFunc;
    handleGetInfo(handle, LRI_PROGRESSCB, &progressFunc);

    addCountmeFlag(handle);

    std::unique_ptr<LrResult> result;
    bool ret;
    bool badGPG = false;
    do {
        if (callbacks && progressFunc)
            callbacks->start(
                !conf->name().getValue().empty() ? conf->name().getValue().c_str() :
                (!id.empty() ? id.c_str() : "unknown")
            );

        GError * errP{nullptr};
        result.reset(lr_result_init());
        ret = lr_handle_perform(handle, result.get(), &errP);
        std::unique_ptr<GError> err(errP);

        if (callbacks && progressFunc)
            callbacks->end();

        if (ret || badGPG || errP->code != LRE_BADGPG) {
            if (!ret) {
                std::string source;
                if (conf->metalink().empty() || (source=conf->metalink().getValue()).empty()) {
                    if (conf->mirrorlist().empty() || (source=conf->mirrorlist().getValue()).empty()) {
                        bool first = true;
                        for (const auto & url : conf->baseurl().getValue()) {
                            if (first)
                                first = false;
                            else
                                source += ", ";
                            source += url;
                        }
                    }
                }
                throw LrExceptionWithSourceUrl(err->code, err->message, source);
            }
            break;
        }
        badGPG = true;
        importRepoKeys();
        dnf_remove_recursive((destDirectory + "/" + METADATA_RELATIVE_DIR).c_str(), NULL);
    } while (true);

    return result;
}

bool Repo::Impl::loadCache(bool throwExcept, bool ignoreMissing)
{
    std::unique_ptr<LrHandle> h(lrHandleInitLocal());
    std::unique_ptr<LrResult> r;

    if (ignoreMissing) {
        handleSetOpt(h.get(), LRO_IGNOREMISSING, 1L);
    }

    // Fetch data
    try {
        r = lrHandlePerform(h.get(), getCachedir(), conf->repo_gpgcheck().getValue());
    } catch (std::exception & ex) {
        if (throwExcept)
            throw;
        return false;
    }

    char **mirrors;
    LrYumRepo *yum_repo;
    LrYumRepoMd *yum_repomd;
    handleGetInfo(h.get(), LRI_MIRRORS, &mirrors);
    resultGetInfo(r.get(), LRR_YUM_REPO, &yum_repo);
    resultGetInfo(r.get(), LRR_YUM_REPOMD, &yum_repomd);

    // Populate repo
    repomdFn = yum_repo->repomd;
    metadataPaths.clear();
    for (auto *elem = yum_repo->paths; elem; elem = g_slist_next(elem)) {
        if (elem->data) {
            auto yumrepopath = static_cast<LrYumRepoPath *>(elem->data);
            metadataPaths.emplace(yumrepopath->type, yumrepopath->path);
        }
    }

    content_tags.clear();
    for (auto elem = yum_repomd->content_tags; elem; elem = g_slist_next(elem)) {
        if (elem->data)
            content_tags.emplace_back(static_cast<const char *>(elem->data));
    }

    distro_tags.clear();
    for (auto elem = yum_repomd->distro_tags; elem; elem = g_slist_next(elem)) {
        if (elem->data) {
            auto distroTag = static_cast<LrYumDistroTag *>(elem->data);
            if (distroTag->tag) {
                std::string cpeid_str;
                if (distroTag->cpeid) {
                    cpeid_str = distroTag->cpeid;
                }
                distro_tags.emplace_back(std::move(cpeid_str), distroTag->tag);
            }
        }
    }

    metadata_locations.clear();
    for (auto elem = yum_repomd->records; elem; elem = g_slist_next(elem)) {
        if (elem->data) {
            auto rec = static_cast<LrYumRepoMdRecord *>(elem->data);
            metadata_locations.emplace_back(rec->type, rec->location_href);
        }
    }

    if (auto cRevision = yum_repomd->revision) {
        revision = cRevision;
    }
    maxTimestamp = lr_yum_repomd_get_highest_timestamp(yum_repomd, NULL);

    // Load timestamp unless explicitly expired
    if (timestamp != 0) {
        timestamp = mtime(getMetadataPath(MD_TYPE_PRIMARY).c_str());
    }
    g_strfreev(this->mirrors);
    this->mirrors = mirrors;
    return true;
}

void Repo::Impl::addCountmeFlag(LrHandle *handle) {
    /*
     * The countme flag will be added once (and only once) in every position of
     * a sliding time window (COUNTME_WINDOW) that starts at COUNTME_OFFSET and
     * moves along the time axis, by one length at a time, in such a way that
     * the current point in time always stays within:
     *
     * UNIX epoch                    now
     * |                             |
     * |---*-----|-----|-----|-----[-*---]---> time
     *     |                       ~~~~~~~
     *     COUNTME_OFFSET          COUNTME_WINDOW
     *
     * This is to align the time window with an absolute point in time rather
     * than the last counting event (which could facilitate tracking across
     * multiple such events).
     */
    auto logger(Log::getLogger());

    // Bail out if not counting or not running as root (since the persistdir is
    // only root-writable)
    if (!conf->countme().getValue() || getuid() != 0)
        return;

    // Bail out if not a remote handle
    long local;
    handleGetInfo(handle, LRI_LOCAL, &local);
    if (local)
        return;

    // Bail out if no metalink or mirrorlist is defined
    auto & metalink = conf->metalink();
    auto & mirrorlist = conf->mirrorlist();
    if ((metalink.empty()   || metalink.getValue().empty()) &&
        (mirrorlist.empty() || mirrorlist.getValue().empty()))
        return;

    // Load the cookie
    std::string fname = getPersistdir() + "/" + COUNTME_COOKIE;
    int ver = COUNTME_VERSION;      // file format version (for future use)
    time_t epoch = 0;               // position of first-ever counted window
    time_t win = COUNTME_OFFSET;    // position of last counted window
    int budget = -1;                // budget for this window (-1 = generate)
    std::ifstream(fname) >> ver >> epoch >> win >> budget;

    // Bail out if the window has not advanced since
    time_t now = time(NULL);
    time_t delta = now - win;
    if (delta < COUNTME_WINDOW) {
        logger->debug(tfm::format("countme: no event for %s: window already counted", id));
        return;
    }

    // Evenly distribute the probability of the counting event over the first N
    // requests in this window (where N = COUNTME_BUDGET), by defining a random
    // "budget" of ordinary requests that we first have to spend.  This ensures
    // that no particular request is special and thus no privacy loss is
    // incurred by adding the flag within N requests.
    if (budget < 0)
        budget = numeric::random(1, COUNTME_BUDGET);
    budget--;
    if (!budget) {
        // Budget exhausted, counting!

        // Compute the position of this window
        win = now - (delta % COUNTME_WINDOW);
        if (!epoch)
            epoch = win;
        // Window step (0 at epoch)
        int step = (win - epoch) / COUNTME_WINDOW;

        // Compute the bucket we are in
        unsigned int i;
        for (i = 0; i < COUNTME_BUCKETS.size(); ++i)
            if (step < COUNTME_BUCKETS[i])
                break;
        int bucket = i + 1;  // Buckets are indexed from 1

        // Set the flag
        std::string flag = "countme=" + std::to_string(bucket);
        handleSetOpt(handle, LRO_ONETIMEFLAG, flag.c_str());
        logger->debug(tfm::format("countme: event triggered for %s: bucket %i", id, bucket));

        // Request a new budget
        budget = -1;
    } else {
        logger->debug(tfm::format("countme: no event for %s: budget to spend: %i", id, budget));
    }

    // Save the cookie
    std::ofstream(fname) << COUNTME_VERSION << " " << epoch << " " << win
                         << " " << budget;
}

// Use metalink to check whether our metadata are still current.
bool Repo::Impl::isMetalinkInSync()
{
    auto logger(Log::getLogger());
    char tmpdir[] = "/tmp/tmpdir.XXXXXX";
    if (!mkdtemp(tmpdir)) {
        const char * errTxt = strerror(errno);
        throw RepoError(tfm::format(_("Cannot create repo temporary directory \"%s\": %s"),
                                      tmpdir, errTxt));
    }
    Finalizer tmpDirRemover([&tmpdir](){
        dnf_remove_recursive(tmpdir, NULL);
    });

    std::unique_ptr<LrHandle> h(lrHandleInitRemote(tmpdir));

    handleSetOpt(h.get(), LRO_FETCHMIRRORS, 1L);
    auto r = lrHandlePerform(h.get(), tmpdir, false);
    LrMetalink * metalink;
    handleGetInfo(h.get(), LRI_METALINK, &metalink);
    if (!metalink) {
        logger->debug(tfm::format(_("reviving: repo '%s' skipped, no metalink."), id));
        return false;
    }

    // check all recognized hashes
    auto chksumFree = [](Chksum * ptr){solv_chksum_free(ptr, nullptr);};
    struct hashInfo {
        const LrMetalinkHash * lrMetalinkHash;
        std::unique_ptr<Chksum, decltype(chksumFree)> chksum;
    };
    std::vector<hashInfo> hashes;
    for (auto hash = metalink->hashes; hash; hash = hash->next) {
        auto lrMetalinkHash = static_cast<const LrMetalinkHash *>(hash->data);
        for (auto algorithm : RECOGNIZED_CHKSUMS) {
            if (strcmp(lrMetalinkHash->type, algorithm) == 0)
                hashes.push_back({lrMetalinkHash, {nullptr, chksumFree}});
        }
    }
    if (hashes.empty()) {
        logger->debug(tfm::format(_("reviving: repo '%s' skipped, no usable hash."), id));
        return false;
    }

    for (auto & hash : hashes) {
        auto chkType = solv_chksum_str2type(hash.lrMetalinkHash->type);
        hash.chksum.reset(solv_chksum_create(chkType));
    }

    std::ifstream repomd(repomdFn, std::ifstream::binary);
    char buf[4096];
    int readed;
    while ((readed = repomd.readsome(buf, sizeof(buf))) > 0) {
        for (auto & hash : hashes)
            solv_chksum_add(hash.chksum.get(), buf, readed);
    }

    for (auto & hash : hashes) {
        int chksumLen;
        auto chksum = solv_chksum_get(hash.chksum.get(), &chksumLen);
        char chksumHex[chksumLen * 2 + 1];
        solv_bin2hex(chksum, chksumLen, chksumHex);
        if (strcmp(chksumHex, hash.lrMetalinkHash->value) != 0) {
            logger->debug(tfm::format(_("reviving: failed for '%s', mismatched %s sum."),
                                      id, hash.lrMetalinkHash->type));
            return false;
        }
    }

    logger->debug(tfm::format(_("reviving: '%s' can be revived - metalink checksums match."), id));
    return true;
}

// Use repomd to check whether our metadata are still current.
bool Repo::Impl::isRepomdInSync()
{
    auto logger(Log::getLogger());
    LrYumRepo *yum_repo;
    char tmpdir[] = "/tmp/tmpdir.XXXXXX";
    if (!mkdtemp(tmpdir)) {
        const char * errTxt = strerror(errno);
        throw RepoError(tfm::format(_("Cannot create repo temporary directory \"%s\": %s"),
                                      tmpdir, errTxt));
    }
    Finalizer tmpDirRemover([&tmpdir](){
        dnf_remove_recursive(tmpdir, NULL);
    });

    const char *dlist[] = LR_YUM_REPOMDONLY;

    std::unique_ptr<LrHandle> h(lrHandleInitRemote(tmpdir));

    handleSetOpt(h.get(), LRO_YUMDLIST, dlist);
    auto r = lrHandlePerform(h.get(), tmpdir, conf->repo_gpgcheck().getValue());
    resultGetInfo(r.get(), LRR_YUM_REPO, &yum_repo);

    auto same = haveFilesSameContent(repomdFn.c_str(), yum_repo->repomd);
    if (same)
        logger->debug(tfm::format(_("reviving: '%s' can be revived - repomd matches."), id));
    else
        logger->debug(tfm::format(_("reviving: failed for '%s', mismatched repomd."), id));
    return same;
}

bool Repo::Impl::isInSync()
{
    if (!conf->metalink().empty() && !conf->metalink().getValue().empty())
        return isMetalinkInSync();
    return isRepomdInSync();
}



void Repo::Impl::fetch(const std::string & destdir, std::unique_ptr<LrHandle> && h)
{
    auto repodir = destdir + "/" + METADATA_RELATIVE_DIR;
    if (g_mkdir_with_parents(destdir.c_str(), 0755) == -1) {
        const char * errTxt = strerror(errno);
        throw RepoError(tfm::format(_("Cannot create repo destination directory \"%s\": %s"),
                                      destdir, errTxt));
    }
    auto tmpdir = destdir + "/tmpdir.XXXXXX";
    if (!mkdtemp(&tmpdir.front())) {
        const char * errTxt = strerror(errno);
        throw RepoError(tfm::format(_("Cannot create repo temporary directory \"%s\": %s"),
                                      tmpdir.c_str(), errTxt));
    }
    Finalizer tmpDirRemover([&tmpdir](){
        dnf_remove_recursive(tmpdir.c_str(), NULL);
    });
    auto tmprepodir = tmpdir + "/" + METADATA_RELATIVE_DIR;

    handleSetOpt(h.get(), LRO_DESTDIR, tmpdir.c_str());
    auto r = lrHandlePerform(h.get(), tmpdir, conf->repo_gpgcheck().getValue());

    dnf_remove_recursive(repodir.c_str(), NULL);
    if (g_mkdir_with_parents(repodir.c_str(), 0755) == -1) {
        const char * errTxt = strerror(errno);
        throw RepoError(tfm::format(_("Cannot create directory \"%s\": %s"),
                                      repodir, errTxt));
    }
    // move all downloaded object from tmpdir to destdir
    if (auto * dir = opendir(tmpdir.c_str())) {
        Finalizer tmpDirRemover([dir](){ closedir(dir); });
        while (auto ent = readdir(dir)) {
            auto elName = ent->d_name;
            if (elName[0] == '.' && (elName[1] == '\0' || (elName[1] == '.' && elName[2] == '\0'))) {
                continue;
            }
            auto targetElement = destdir + "/" + elName;
            if (filesystem::exists(targetElement)) {
                if (filesystem::isDIR(targetElement.c_str())) {
                    dnf_remove_recursive(targetElement.c_str(), NULL);
                } else {
                    dnf_ensure_file_unlinked(targetElement.c_str(), NULL);
                }
            }
            auto tempElement = tmpdir + "/" + elName;
            GError * error = NULL;
            if (!dnf_move_recursive(tempElement.c_str(), targetElement.c_str(), &error)) {
                std::string errTxt = tfm::format(
                    _("Cannot rename directory \"%s\" to \"%s\": %s"),
                    tempElement, targetElement, error->message);
                g_error_free(error);
                throw RepoError(errTxt);
            }
        }
    }
}

void Repo::Impl::downloadMetadata(const std::string & destdir)
{
    std::unique_ptr<LrHandle> h(lrHandleInitRemote(nullptr));
    handleSetOpt(h.get(), LRO_YUMDLIST, LR_RPMMD_FULL);
    fetch(destdir, std::move(h));
}

bool Repo::Impl::load()
{
    auto logger(Log::getLogger());
    try {
        if (!getMetadataPath(MD_TYPE_PRIMARY).empty() || loadCache(false)) {
            resetMetadataExpired();
            if (!expired || syncStrategy == SyncStrategy::ONLY_CACHE || syncStrategy == SyncStrategy::LAZY) {
                logger->debug(tfm::format(_("repo: using cache for: %s"), id));
                return false;
            }

            if (isInSync()) {
                // the expired metadata still reflect the origin:
                utimes(getMetadataPath(MD_TYPE_PRIMARY).c_str(), NULL);
                expired = false;
                return true;
            }
        }
        if (syncStrategy == SyncStrategy::ONLY_CACHE) {
            auto msg = tfm::format(_("Cache-only enabled but no cache for '%s'"), id);
            throw RepoError(msg);
        }

        logger->debug(tfm::format(_("repo: downloading from remote: %s"), id));
        const auto cacheDir = getCachedir();
        fetch(cacheDir, lrHandleInitRemote(nullptr));
        timestamp = -1;
        loadCache(true);
        fresh = true;
    } catch (const LrExceptionWithSourceUrl & e) {
        auto msg = tfm::format(_("Failed to download metadata for repo '%s': %s"), id, e.what());
        throw RepoError(msg);
    }
    expired = false;
    return true;
}

std::string Repo::Impl::getHash() const
{
    std::string tmp;
    if (conf->metalink().empty() || (tmp=conf->metalink().getValue()).empty()) {
        if (conf->mirrorlist().empty() || (tmp=conf->mirrorlist().getValue()).empty()) {
            if (!conf->baseurl().getValue().empty())
                tmp = conf->baseurl().getValue()[0];
            if (tmp.empty())
                tmp = id;
        }
    }

    auto chksumObj = solv_chksum_create(REPOKEY_TYPE_SHA256);
    solv_chksum_add(chksumObj, tmp.c_str(), tmp.length());
    int chksumLen;
    auto chksum = solv_chksum_get(chksumObj, &chksumLen);
    static constexpr int USE_CHECKSUM_BYTES = 8;
    if (chksumLen < USE_CHECKSUM_BYTES) {
        solv_chksum_free(chksumObj, nullptr);
        throw Exception(_("getCachedir(): Computation of SHA256 failed"));
    }
    char chksumCStr[USE_CHECKSUM_BYTES * 2 + 1];
    solv_bin2hex(chksum, USE_CHECKSUM_BYTES, chksumCStr);
    solv_chksum_free(chksumObj, nullptr);

    return id + "-" + chksumCStr;
}

std::string Repo::Impl::getCachedir() const
{
    if (conf->basecachedir().empty()) {
        throw Exception(tfm::format(_("repo '%s': 'basecachedir' is not set"), id));
    }
    auto repodir(conf->basecachedir().getValue());
    if (repodir.back() != '/')
        repodir.push_back('/');
    return repodir + getHash();
}

std::string Repo::Impl::getPersistdir() const
{
    auto persdir(conf->getMainConfig().persistdir().getValue());
    if (persdir.back() != '/')
        persdir.push_back('/');
    std::string result = persdir + "repos/" + getHash();
    if (g_mkdir_with_parents(result.c_str(), 0755) == -1) {
        const char * errTxt = strerror(errno);
        throw RepoError(tfm::format(_("Cannot create persistdir \"%s\": %s"),
                                    result, errTxt));
    }
    return result;
}

int Repo::Impl::getAge() const
{
    return time(NULL) - mtime(getMetadataPath(MD_TYPE_PRIMARY).c_str());
}

void Repo::Impl::expire()
{
    expired = true;
    timestamp = 0;
}

bool Repo::Impl::isExpired() const
{
    if (expired)
        // explicitly requested expired state
        return true;
    if (conf->metadata_expire().getValue() == -1)
        return false;
    return getAge() > conf->metadata_expire().getValue();
}

int Repo::Impl::getExpiresIn() const
{
    return conf->metadata_expire().getValue() - getAge();
}

void Repo::Impl::downloadUrl(const char * url, int fd)
{
    if (callbacks)
        callbacks->start(
            !conf->name().getValue().empty() ? conf->name().getValue().c_str() :
            (!id.empty() ? id.c_str() : "unknown")
        );

    GError * errP{nullptr};
    lr_download_url(getCachedHandle(), url, fd, &errP);
    std::unique_ptr<GError> err(errP);

    if (callbacks)
        callbacks->end();

    if (err)
        throw LrExceptionWithSourceUrl(err->code, err->message, url);
}

void Repo::Impl::setHttpHeaders(const char * headers[])
{
    if (!headers) {
        httpHeaders.reset();
        return;
    }
    size_t headersCount = 0;
    while (headers[headersCount])
        ++headersCount;
    httpHeaders.reset(new char*[headersCount + 1]{});
    for (size_t i = 0; i < headersCount; ++i) {
        httpHeaders[i] = new char[strlen(headers[i]) + 1];
        strcpy(httpHeaders[i], headers[i]);
    }
}

const char * const * Repo::Impl::getHttpHeaders() const
{
    return httpHeaders.get();
}

bool Repo::fresh()
{
    return pImpl->fresh;
}

void Repo::Impl::resetMetadataExpired()
{
    if (expired || conf->metadata_expire().getValue() == -1)
        return;
    if (conf->getMainConfig().check_config_file_age().getValue() &&
        !repoFilePath.empty() &&
        mtime(repoFilePath.c_str()) > mtime(getMetadataPath(MD_TYPE_PRIMARY).c_str()))
        expired = true;
    else
        expired = getAge() > conf->metadata_expire().getValue();
}


/* Returns a librepo handle, set as per the repo options
   Note that destdir is None, and the handle is cached.*/
LrHandle * Repo::Impl::getCachedHandle()
{
    if (!handle)
        handle = lrHandleInitRemote(nullptr);
    handleSetOpt(handle.get(), LRO_HTTPHEADER, httpHeaders.get());
    return handle.get();
}

void Repo::Impl::attachLibsolvRepo(LibsolvRepo * libsolvRepo)
{
    std::lock_guard<std::mutex> guard(attachLibsolvMutex);

    if (this->libsolvRepo)
        // A libsolvRepo was attached to this object before. Remove it's reference to this object.
        this->libsolvRepo->appdata = nullptr;
    else
        // The libsolvRepo will reference this object. Increase reference counter.
        ++nrefs;

    libsolvRepo->appdata = owner; // The libsolvRepo references back to us.
    libsolvRepo->subpriority = -owner->getCost();
    libsolvRepo->priority = -owner->getPriority();
    this->libsolvRepo = libsolvRepo;
}

void Repo::Impl::detachLibsolvRepo()
{
    attachLibsolvMutex.lock();
    if (!libsolvRepo) {
        // Nothing to do, libsolvRepo is not attached.
        attachLibsolvMutex.unlock();
        return;
    }

    libsolvRepo->appdata = nullptr; // Removes reference to this object from libsolvRepo.
    this->libsolvRepo = nullptr;

    if (--nrefs <= 0) {
        // There is no reference to this object, we are going to destroy it.
        // Mutex is part of this object, we must unlock it before destroying.
        attachLibsolvMutex.unlock();
        delete owner;
    } else
        attachLibsolvMutex.unlock();
}

LrAuth Repo::Impl::stringToProxyAuthMethods(const std::string & proxyAuthMethodStr) noexcept
{
    auto proxyAuthMethods = LR_AUTH_ANY;
    for (auto & auth : PROXYAUTHMETHODS) {
        if (proxyAuthMethodStr == auth.name) {
            proxyAuthMethods = auth.code;
            break;
        }
    }
    return proxyAuthMethods;
}

void Repo::setMaxMirrorTries(int maxMirrorTries)
{
    pImpl->maxMirrorTries = maxMirrorTries;
}

int Repo::getTimestamp() const
{
    return pImpl->timestamp;
}

int Repo::getMaxTimestamp()
{
    return pImpl->maxTimestamp;
}

void Repo::setPreserveRemoteTime(bool preserveRemoteTime)
{
    pImpl->preserveRemoteTime = preserveRemoteTime;
}

bool Repo::getPreserveRemoteTime() const
{
    return pImpl->preserveRemoteTime;
}

const std::vector<std::string> & Repo::getContentTags()
{
    return pImpl->content_tags;
}

const std::vector<std::pair<std::string, std::string>> & Repo::getDistroTags()
{
    return pImpl->distro_tags;
}

const std::vector<std::pair<std::string, std::string>> Repo::getMetadataLocations() const
{
    return pImpl->metadata_locations;
}

const std::string & Repo::getRevision() const
{
    return pImpl->revision;
}

std::string Repo::getCachedir() const
{
    return pImpl->getCachedir();
}

void Repo::setRepoFilePath(const std::string & path)
{
    pImpl->repoFilePath = path;
}

const std::string & Repo::getRepoFilePath() const noexcept
{
    return pImpl->repoFilePath;
}

void Repo::setSyncStrategy(SyncStrategy strategy)
{
    pImpl->syncStrategy = strategy;
}

Repo::SyncStrategy Repo::getSyncStrategy() const noexcept
{
    return pImpl->syncStrategy;
}

void Repo::downloadUrl(const char * url, int fd)
{
    pImpl->downloadUrl(url, fd);
}

void Repo::setHttpHeaders(const char * headers[])
{
    pImpl->setHttpHeaders(headers);
}

const char * const * Repo::getHttpHeaders() const
{
    return pImpl->getHttpHeaders();
}

std::vector<std::string> Repo::getMirrors() const
{
    std::vector<std::string> mirrors;
    if (pImpl->mirrors) {
        for (auto mirror = pImpl->mirrors; *mirror; ++mirror)
            mirrors.emplace_back(*mirror);
    }
    return mirrors;
}

int PackageTargetCB::end(TransferStatus status, const char * msg) { return 0; }
int PackageTargetCB::progress(double totalToDownload, double downloaded) { return 0; }
int PackageTargetCB::mirrorFailure(const char *msg, const char *url) { return 0; }

class PackageTarget::Impl {
public:
    Impl(Repo * repo, const char * relativeUrl, const char * dest, int chksType,
         const char * chksum, int64_t expectedSize, const char * baseUrl, bool resume,
         int64_t byteRangeStart, int64_t byteRangeEnd, PackageTargetCB * callbacks);

    Impl(ConfigMain * cfg, const char * relativeUrl, const char * dest, int chksType,
         const char * chksum, int64_t expectedSize, const char * baseUrl, bool resume,
         int64_t byteRangeStart, int64_t byteRangeEnd, PackageTargetCB * callbacks,
         const char * httpHeaders[]);

    void download();

    ~Impl();

    PackageTargetCB * callbacks;

    std::unique_ptr<LrPackageTarget> lrPkgTarget;

private:
    void init(LrHandle * handle, const char * relativeUrl, const char * dest, int chksType,
              const char * chksum, int64_t expectedSize, const char * baseUrl, bool resume,
              int64_t byteRangeStart, int64_t byteRangeEnd);

    static int endCB(void * data, LrTransferStatus status, const char * msg);
    static int progressCB(void * data, double totalToDownload, double downloaded);
    static int mirrorFailureCB(void * data, const char * msg, const char * url);

    std::unique_ptr<LrHandle> lrHandle;

};


int PackageTarget::Impl::endCB(void * data, LrTransferStatus status, const char * msg)
{
    if (!data)
        return 0;
    auto cbObject = static_cast<PackageTargetCB *>(data);
    return cbObject->end(static_cast<PackageTargetCB::TransferStatus>(status), msg);
}

int PackageTarget::Impl::progressCB(void * data, double totalToDownload, double downloaded)
{
    if (!data)
        return 0;
    auto cbObject = static_cast<PackageTargetCB *>(data);
    return cbObject->progress(totalToDownload, downloaded);
}

int PackageTarget::Impl::mirrorFailureCB(void * data, const char * msg, const char * url)
{
    if (!data)
        return 0;
    auto cbObject = static_cast<PackageTargetCB *>(data);
    return cbObject->mirrorFailure(msg, url);
}


static LrHandle * newHandle(ConfigMain * conf)
{
    LrHandle *h = lr_handle_init();
    const char * user_agent = USER_AGENT;
    // see dnf.repo.Repo._handle_new_remote() how to pass
    if (conf) {
        user_agent = conf->user_agent().getValue().c_str();
        setHandle(h, *conf);
    }
    handleSetOpt(h, LRO_USERAGENT, user_agent);
    return h;
}

PackageTarget::ChecksumType PackageTarget::checksumType(const std::string & name)
{
    return static_cast<ChecksumType>(lr_checksum_type(name.c_str()));
}

void PackageTarget::downloadPackages(std::vector<PackageTarget *> & targets, bool failFast)
{
    // Convert vector to GSList
    GSList * list{nullptr};
    for (auto it = targets.rbegin(); it != targets.rend(); ++it)
        list = g_slist_prepend(list, (*it)->pImpl->lrPkgTarget.get());
    std::unique_ptr<GSList, decltype(&g_slist_free)> listGuard(list, &g_slist_free);

    LrPackageDownloadFlag flags = static_cast<LrPackageDownloadFlag>(0);
    if (failFast)
        flags = static_cast<LrPackageDownloadFlag>(flags | LR_PACKAGEDOWNLOAD_FAILFAST);

    GError * errP{nullptr};
    lr_download_packages(list, flags, &errP);
    std::unique_ptr<GError> err(errP);

    if (err)
        throwException(std::move(err));
}


PackageTarget::Impl::~Impl() {}

PackageTarget::Impl::Impl(Repo * repo, const char * relativeUrl, const char * dest, int chksType,
                          const char * chksum, int64_t expectedSize, const char * baseUrl, bool resume,
                          int64_t byteRangeStart, int64_t byteRangeEnd, PackageTargetCB * callbacks)
: callbacks(callbacks)
{
    init(repo->pImpl->getCachedHandle(), relativeUrl, dest, chksType, chksum, expectedSize,
         baseUrl, resume, byteRangeStart, byteRangeEnd);
}

PackageTarget::Impl::Impl(ConfigMain * cfg, const char * relativeUrl, const char * dest, int chksType,
                          const char * chksum, int64_t expectedSize, const char * baseUrl, bool resume,
                          int64_t byteRangeStart, int64_t byteRangeEnd, PackageTargetCB * callbacks,
                          const char * httpHeaders[])
: callbacks(callbacks)
{
    lrHandle.reset(newHandle(cfg));
    handleSetOpt(lrHandle.get(), LRO_HTTPHEADER, httpHeaders);
    handleSetOpt(lrHandle.get(), LRO_REPOTYPE, LR_YUMREPO);
    init(lrHandle.get(), relativeUrl, dest, chksType, chksum, expectedSize, baseUrl, resume,
         byteRangeStart, byteRangeEnd);
}

void PackageTarget::Impl::init(LrHandle * handle, const char * relativeUrl, const char * dest, int chksType,
                               const char * chksum, int64_t expectedSize, const char * baseUrl, bool resume,
                               int64_t byteRangeStart, int64_t byteRangeEnd)
{
    LrChecksumType lrChksType = static_cast<LrChecksumType>(chksType);

    if (resume && byteRangeStart) {
        auto msg = _("resume cannot be used simultaneously with the byterangestart param");
        throw Exception(msg);
    }

    GError * errP{nullptr};

    std::string encodedUrl = relativeUrl;
    if (encodedUrl.find("://") == std::string::npos) {
        encodedUrl = urlEncode(encodedUrl, "/");
    }

    lrPkgTarget.reset(lr_packagetarget_new_v3(handle, encodedUrl.c_str(), dest, lrChksType, chksum,
                                              expectedSize, baseUrl, resume, progressCB, callbacks, endCB,
                                              mirrorFailureCB, byteRangeStart, byteRangeEnd, &errP));
    std::unique_ptr<GError> err(errP);

    if (!lrPkgTarget) {
        auto msg = tfm::format(_("PackageTarget initialization failed: %s"), err->message);
        throw Exception(msg);
    }
}

PackageTarget::PackageTarget(Repo * repo, const char * relativeUrl, const char * dest, int chksType,
                             const char * chksum, int64_t expectedSize, const char * baseUrl, bool resume,
                             int64_t byteRangeStart, int64_t byteRangeEnd, PackageTargetCB * callbacks)
: pImpl(new Impl(repo, relativeUrl, dest, chksType, chksum, expectedSize, baseUrl, resume,
                 byteRangeStart, byteRangeEnd, callbacks))
{}

PackageTarget::PackageTarget(ConfigMain * cfg, const char * relativeUrl, const char * dest, int chksType,
                             const char * chksum, int64_t expectedSize, const char * baseUrl, bool resume,
                             int64_t byteRangeStart, int64_t byteRangeEnd, PackageTargetCB * callbacks,
                             const char * httpHeaders[])
: pImpl(new Impl(cfg, relativeUrl, dest, chksType, chksum, expectedSize, baseUrl, resume,
                 byteRangeStart, byteRangeEnd, callbacks, httpHeaders))
{}


PackageTarget::~PackageTarget() {}

PackageTargetCB * PackageTarget::getCallbacks()
{
    return pImpl->callbacks;
}

const char * PackageTarget::getErr()
{
    return pImpl->lrPkgTarget->err;
}

void Downloader::downloadURL(ConfigMain * cfg, const char * url, int fd)
{
    std::unique_ptr<LrHandle> lrHandle(newHandle(cfg));
    GError * errP{nullptr};
    lr_download_url(lrHandle.get(), url, fd, &errP);
    std::unique_ptr<GError> err(errP);

    if (err)
        throwException(std::move(err));
}

// ============ librepo logging ===========

#define LR_LOGDOMAIN "librepo"

class LrHandleLogData {
public:
    std::string filePath;
    long uid;
    FILE *fd;
    bool used{false};
    guint handlerId;

    ~LrHandleLogData();
};

LrHandleLogData::~LrHandleLogData()
{
    if (used)
        g_log_remove_handler(LR_LOGDOMAIN, handlerId);
    fclose(fd);
}

static std::list<std::unique_ptr<LrHandleLogData>> lrLogDatas;
static std::mutex lrLogDatasMutex;

static const Logger::Level lrLogLevelFlagToLevel(GLogLevelFlags logLevelFlag)
{
    if (logLevelFlag & G_LOG_LEVEL_ERROR) {
        return Logger::Level::ERROR;
    }
    if (logLevelFlag & G_LOG_LEVEL_CRITICAL) {
        return Logger::Level::WARNING;
    }
    if (logLevelFlag & G_LOG_LEVEL_WARNING) {
        return Logger::Level::WARNING;
    }
    if (logLevelFlag & G_LOG_LEVEL_MESSAGE) {
        return Logger::Level::NOTICE;
    }
    if (logLevelFlag & G_LOG_LEVEL_INFO) {
        return Logger::Level::INFO;
    }
    if (logLevelFlag & G_LOG_LEVEL_DEBUG) {
        return Logger::Level::DEBUG;
    }
    return Logger::Level::TRACE;
}

static void librepoLogCB(G_GNUC_UNUSED const gchar *log_domain, GLogLevelFlags log_level,
                         const char *msg, gpointer user_data) noexcept
{
    auto logger(Log::getLogger());
    logger->write(Logger::LOG_SOURCE_LIBREPO, lrLogLevelFlagToLevel(log_level), msg);
}

long LibrepoLog::addHandler(const std::string & filePath, bool debug)
{
    static long uid = 0;

    // Open the file
    FILE *fd = fopen(filePath.c_str(), "a");
    if (!fd)
        throw RepoError(tfm::format(_("Cannot open %s: %s"), filePath, g_strerror(errno)));

    // Setup user data
    std::unique_ptr<LrHandleLogData> data(new LrHandleLogData);
    data->filePath = filePath;
    data->fd = fd;

    // Set handler
    GLogLevelFlags log_mask = debug ? G_LOG_LEVEL_MASK : static_cast<GLogLevelFlags>(
        G_LOG_LEVEL_INFO |
        G_LOG_LEVEL_MESSAGE |
        G_LOG_LEVEL_WARNING |
        G_LOG_LEVEL_CRITICAL |
        G_LOG_LEVEL_ERROR);

    data->handlerId = g_log_set_handler(LR_LOGDOMAIN, log_mask, librepoLogCB, data.get());
    data->used = true;

    // Save user data (in a thread safe way)
    {
        std::lock_guard<std::mutex> guard(lrLogDatasMutex);

        // Get unique ID of the handler
        data->uid = ++uid;

        // Append the data to the global list
        lrLogDatas.push_front(std::move(data));
    }

    // Log librepo version and current time (including timezone)
    lr_log_librepo_summary();

    // Return unique id of the handler data
    return uid;
}

void LibrepoLog::removeHandler(long uid)
{
    std::lock_guard<std::mutex> guard(lrLogDatasMutex);

    // Search for the corresponding LogFileData
    auto it = lrLogDatas.begin();
    for (; it != lrLogDatas.end() && (*it)->uid != uid; ++it);
    if (it == lrLogDatas.end())
        throw Exception(tfm::format(_("Log handler with id %ld doesn't exist"), uid));

    // Remove the handler and free the data
    lrLogDatas.erase(it);
}

void LibrepoLog::removeAllHandlers()
{
    std::lock_guard<std::mutex> guard(lrLogDatasMutex);
    lrLogDatas.clear();
}

Repo::Impl * repoGetImpl(Repo * repo)
{
    return repo->pImpl.get();
}

}

// hawkey
#include "../hy-repo-private.hpp"

void
repo_internalize_all_trigger(Pool *pool)
{
    int i;
    Repo *repo;

    FOR_REPOS(i, repo)
        repo_internalize_trigger(repo);
}

void
repo_internalize_trigger(Repo * repo)
{
    if (!repo)
        return;

    if (auto hrepo = static_cast<HyRepo>(repo->appdata)) {
        // HyRepo is attached. The hint needs_internalizing will be used.
        auto repoImpl = libdnf::repoGetImpl(hrepo);
        assert(repoImpl->libsolvRepo == repo);
        if (!repoImpl->needs_internalizing)
            return;
        repoImpl->needs_internalizing = false;
    }

    repo_internalize(repo);
}

void
repo_update_state(HyRepo repo, enum _hy_repo_repodata which,
                  enum _hy_repo_state state)
{
    auto repoImpl = libdnf::repoGetImpl(repo);
    assert(state <= _HY_WRITTEN);
    switch (which) {
    case _HY_REPODATA_FILENAMES:
        repoImpl->state_filelists = state;
        return;
    case _HY_REPODATA_PRESTO:
        repoImpl->state_presto = state;
        return;
    case _HY_REPODATA_UPDATEINFO:
        repoImpl->state_updateinfo = state;
        return;
    case _HY_REPODATA_OTHER:
        repoImpl->state_other = state;
        return;
    default:
        assert(0);
    }
    return;
}

Id
repo_get_repodata(HyRepo repo, enum _hy_repo_repodata which)
{
    auto repoImpl = libdnf::repoGetImpl(repo);
    switch (which) {
    case _HY_REPODATA_FILENAMES:
        return repoImpl->filenames_repodata;
    case _HY_REPODATA_PRESTO:
        return repoImpl->presto_repodata;
    case _HY_REPODATA_UPDATEINFO:
        return repoImpl->updateinfo_repodata;
    case _HY_REPODATA_OTHER:
        return repoImpl->other_repodata;
    default:
        assert(0);
        return 0;
    }
}

void
repo_set_repodata(HyRepo repo, enum _hy_repo_repodata which, Id repodata)
{
    auto repoImpl = libdnf::repoGetImpl(repo);
    switch (which) {
    case _HY_REPODATA_FILENAMES:
        repoImpl->filenames_repodata = repodata;
        return;
    case _HY_REPODATA_PRESTO:
        repoImpl->presto_repodata = repodata;
        return;
    case _HY_REPODATA_UPDATEINFO:
        repoImpl->updateinfo_repodata = repodata;
        return;
    case _HY_REPODATA_OTHER:
        repoImpl->other_repodata = repodata;
        return;
    default:
        assert(0);
        return;
    }
}

// public functions

HyRepo
hy_repo_create(const char *name)
{
    assert(name);
    auto & cfgMain = libdnf::getGlobalMainConfig();
    std::unique_ptr<libdnf::ConfigRepo> cfgRepo(new libdnf::ConfigRepo(cfgMain));
    auto repo = new libdnf::Repo(name, std::move(cfgRepo), libdnf::Repo::Type::COMMANDLINE);
    auto repoImpl = libdnf::repoGetImpl(repo);
    repoImpl->conf->name().set(libdnf::Option::Priority::RUNTIME, name);
    return repo;
}

int
hy_repo_get_cost(HyRepo repo)
{
    return repo->getCost();
}

int
hy_repo_get_priority(HyRepo repo)
{
    return repo->getPriority();
}

gboolean
hy_repo_get_use_includes(HyRepo repo)
{
  return repo->getUseIncludes();
}

guint
hy_repo_get_n_solvables(HyRepo repo)
{
  return (guint)libdnf::repoGetImpl(repo)->libsolvRepo->nsolvables;
}

void
hy_repo_set_cost(HyRepo repo, int value)
{
    auto repoImpl = libdnf::repoGetImpl(repo);
    repoImpl->conf->cost().set(libdnf::Option::Priority::RUNTIME, value);
    if (repoImpl->libsolvRepo)
        repoImpl->libsolvRepo->subpriority = -value;
}

void
hy_repo_set_priority(HyRepo repo, int value)
{
    auto repoImpl = libdnf::repoGetImpl(repo);
    repoImpl->conf->priority().set(libdnf::Option::Priority::RUNTIME, value);
    if (repoImpl->libsolvRepo)
        repoImpl->libsolvRepo->priority = -value;
}

void
hy_repo_set_use_includes(HyRepo repo, gboolean enabled)
{
    repo->setUseIncludes(enabled);
}

void
hy_repo_set_string(HyRepo repo, int which, const char *str_val)
{
    auto repoImpl = libdnf::repoGetImpl(repo);
    switch (which) {
    case HY_REPO_NAME:
        repoImpl->id = str_val;
        repoImpl->conf->name().set(libdnf::Option::Priority::RUNTIME, str_val);
        break;
    case HY_REPO_MD_FN:
        repoImpl->repomdFn = str_val ? str_val : "";
        break;
    case HY_REPO_PRIMARY_FN:
        repoImpl->metadataPaths[MD_TYPE_PRIMARY] = str_val ? str_val : "";
        break;
    case HY_REPO_FILELISTS_FN:
        repoImpl->metadataPaths[MD_TYPE_FILELISTS] = str_val ? str_val : "";
        break;
    case HY_REPO_PRESTO_FN:
        repoImpl->metadataPaths[MD_TYPE_PRESTODELTA] = str_val ? str_val : "";
        break;
    case HY_REPO_UPDATEINFO_FN:
        repoImpl->metadataPaths[MD_TYPE_UPDATEINFO] = str_val ? str_val : "";
        break;
    case HY_REPO_OTHER_FN:
        repoImpl->metadataPaths[MD_TYPE_OTHER] = str_val ? str_val : "";
        break;
    case MODULES_FN:
        repoImpl->metadataPaths[MD_TYPE_MODULES] = str_val ? str_val : "";
        break;
    default:
        assert(0);
    }
}

const char *
hy_repo_get_string(HyRepo repo, int which)
{
    auto repoImpl = libdnf::repoGetImpl(repo);
    const char * ret;
    switch(which) {
    case HY_REPO_NAME:
        return repoImpl->id.c_str();
    case HY_REPO_MD_FN:
        ret = repoImpl->repomdFn.c_str();
        break;
    case HY_REPO_PRIMARY_FN:
        ret = repoImpl->getMetadataPath(MD_TYPE_PRIMARY).c_str();
        break;
    case HY_REPO_FILELISTS_FN:
        ret = repoImpl->getMetadataPath(MD_TYPE_FILELISTS).c_str();
        break;
    case HY_REPO_PRESTO_FN:
        ret = repoImpl->getMetadataPath(MD_TYPE_PRESTODELTA).c_str();
        break;
    case HY_REPO_UPDATEINFO_FN:
        ret = repoImpl->getMetadataPath(MD_TYPE_UPDATEINFO).c_str();
        break;
    case HY_REPO_OTHER_FN:
        ret = repoImpl->getMetadataPath(MD_TYPE_OTHER).c_str();
        break;
    case MODULES_FN:
        ret = repoImpl->getMetadataPath(MD_TYPE_MODULES).c_str();
        break;
    default:
        return nullptr;
    }
    return ret[0] == '\0' ? nullptr : ret;
}

void
hy_repo_free(HyRepo repo)
{
    auto repoImpl = libdnf::repoGetImpl(repo);
    {
        std::lock_guard<std::mutex> guard(repoImpl->attachLibsolvMutex);
        if (--repoImpl->nrefs > 0)
            return; // There is still a reference to this object. Don't destroy it.
    }
    assert(!repoImpl->libsolvRepo);
    delete repo;
}
