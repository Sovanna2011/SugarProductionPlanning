sap.ui.define([
	"kss/spp/controller/BaseController",
	"sap/ui/model/json/JSONModel",
	"sap/m/MessageToast"
], function (BaseController, JSONModel, MessageToast) {
	"use strict";

	return BaseController.extend("kss.spp.controller.Login", {

		onInit: function () {
			this._login = new JSONModel({
				username: "",
				password: "",
				busy: false,
				error: "",
				demoAccounts: [],
				demoPassword: ""
			});
			this.getView().setModel(this._login, "login");

			this.getRouter().getRoute("login").attachPatternMatched(this._onRouteMatched, this);
		},

		_onRouteMatched: function () {
			// Never leave a password sitting in a field behind a back button.
			this._login.setProperty("/password", "");
			this._login.setProperty("/error", "");

			// Somebody already signed in has no business here.
			var oSession = this.getSession();
			if (oSession.loginRequired && oSession.authenticated) {
				this.getRouter().navTo("dashboard", {}, true);
				return;
			}

			this._loadConfig();
		},

		/**
		 * Asks what this deployment does about sign-in, and which fixture
		 * accounts it holds. Reachable without a session by design: a browser
		 * has to be able to ask before anyone has one.
		 */
		_loadConfig: function () {
			var that = this;
			return this.get("auth/config")
				.then(function (oConfig) {
					var aAccounts = (oConfig.demoAccounts || []).map(function (oAccount) {
						return Object.assign({}, oAccount, {
							rolesText: (oAccount.roles || []).join(", ")
						});
					});
					that._login.setProperty("/demoAccounts", aAccounts);
					that._login.setProperty("/demoPassword", oConfig.demoPassword || "");
				})
				.catch(function () {
					// The login form still works without the shortcuts.
					that._login.setProperty("/demoAccounts", []);
				});
		},

		/** Fills both fields from a demo account so it is one more click. */
		onUseDemoAccount: function (oEvent) {
			var oContext = oEvent.getSource().getBindingContext("login");
			this._login.setProperty("/username", oContext.getProperty("username"));
			this._login.setProperty("/password", this._login.getProperty("/demoPassword"));
			this._login.setProperty("/error", "");
			this.onSignIn();
		},

		onSignIn: function () {
			var that = this;
			var sUsername = (this._login.getProperty("/username") || "").trim();
			var sPassword = this._login.getProperty("/password") || "";

			if (!sUsername || !sPassword) {
				this._login.setProperty("/error", this.getText("enterCredentials"));
				return;
			}

			this._login.setProperty("/busy", true);
			this._login.setProperty("/error", "");

			this.post("auth/login", { username: sUsername, password: sPassword })
				.then(function () {
					// The cookie is set; ask the server who that made us,
					// rather than assuming the login response is the whole
					// answer.
					return that.getOwnerComponent().refreshSession();
				})
				.then(function (oSession) {
					that._login.setProperty("/password", "");
					MessageToast.show(that.getText("signedInAs", [oSession.displayName || oSession.username]));

					if (oSession.mustChangePassword) {
						that.getRouter().navTo("dashboard", {}, true);
						that.onChangePassword();
						return;
					}
					that.getRouter().navTo("dashboard", {}, true);
				})
				.catch(function (err) {
					that._login.setProperty("/password", "");
					that._login.setProperty("/error", (err && err.message) || that.getText("signInFailed"));
				})
				.finally(function () {
					that._login.setProperty("/busy", false);
				});
		},

		/** Only offered when the deployment has no sign-in at all. */
		onContinue: function () {
			this.getRouter().navTo("dashboard", {}, true);
		}
	});
});
