sap.ui.define([
	"sap/ui/core/UIComponent",
	"sap/ui/model/json/JSONModel",
	"sap/ui/Device"
], function (UIComponent, JSONModel, Device) {
	"use strict";

	return UIComponent.extend("kss.spp.Component", {

		metadata: {
			manifest: "json"
		},

		init: function () {
			UIComponent.prototype.init.apply(this, arguments);

			this.setModel(new JSONModel(Device), "device");

			// The app model is owned by the component so the dashboard and the
			// master data screens share one set of filters and master data.

			// The dashboard state: filters, the loaded payload and busy flags.
			this.setModel(new JSONModel({
				busy: false,
				loaded: false,
				error: "",
				filters: {
					factoryId: 0,
					date: null,
					storageType: "",
					storageCode: "",
					productCode: "",
					packagingCode: ""
				},
				factories: [],
				storageTypes: [],
				storageLocations: [],
				products: [],
				packagingTypes: [],
				dashboard: {
					groups: [],
					storages: [],
					alerts: []
				},
				projection: null,
				projectionScopes: [],
				appBusy: false,
				master: {
					storageLocations: [],
					capacities: [],
					packagingTypes: [],
					thresholds: [],
					storageTypes: [],
					products: []
				},
				// Who is signed in, and what they are allowed to do. The
				// permissions come from the server rather than being worked
				// out here, so the buttons the UI shows and the requests the
				// API accepts cannot drift apart.
				session: this._anonymousSession(),
				users: {
					list: [],
					busy: false
				}
			}), "app");

			// Establishing who this is comes before the first screen: a
			// deployment that requires a login should never briefly render a
			// dashboard whose every request is about to be refused.
			var that = this;
			this._pSession = this.refreshSession()
				.catch(function () { /* handled in refreshSession */ })
				.then(function () {
					var oSession = that.getModel("app").getProperty("/session");
					if (oSession.loginRequired && !oSession.authenticated) {
						// Skip the hash the browser arrived with, so the
						// requested screen is not built only to be replaced.
						that.getRouter().initialize(true);
						that.getRouter().navTo("login", {}, true);
						return;
					}
					that.getRouter().initialize();
				});
		},

		/**
		 * Base URL of the planning API. Same origin by default, which is how the
		 * Go server serves this app; override with ?api=http://host:port for a
		 * split deployment.
		 */
		getApiBase: function () {
			var sOverride = new URLSearchParams(window.location.search).get("api");
			return sOverride ? sOverride.replace(/\/$/, "") : "";
		},

		/**
		 * Asks the server who this is. It answers for anonymous callers too,
		 * so this is one request rather than a request and an interpretation
		 * of its failure.
		 */
		refreshSession: function () {
			var that = this;
			return fetch(this.getApiBase() + "/api/v1/auth/me", {
				credentials: "same-origin",
				headers: { "Accept": "application/json" }
			}).then(function (res) {
				return res.ok ? res.json() : null;
			}).then(function (oBody) {
				that.applySession(oBody);
				return that.getModel("app").getProperty("/session");
			}).catch(function (err) {
				// The API is unreachable. Leave the session anonymous; the
				// screens surface the failure when their own calls fail.
				that.applySession(null);
				throw err;
			});
		},

		/** Writes a /auth/me payload into the app model. */
		applySession: function (oBody) {
			var oModel = this.getModel("app");
			if (!oBody) {
				oModel.setProperty("/session", this._anonymousSession());
				return;
			}

			var aRoles = oBody.roles || [];
			oModel.setProperty("/session", {
				loaded: true,
				mode: oBody.mode || "none",
				loginRequired: !!oBody.loginRequired,
				authenticated: !!oBody.authenticated,
				username: oBody.username || "",
				displayName: oBody.displayName || oBody.username || "",
				roles: aRoles,
				rolesText: aRoles.join(", "),
				mustChangePassword: !!oBody.mustChangePassword,
				permissions: oBody.permissions || {}
			});
		},

		/**
		 * Sends the user to the login screen. Called when a request comes back
		 * 401, which means the session ended while the page stayed open.
		 */
		requireSignIn: function () {
			var oModel = this.getModel("app");
			var oSession = oModel.getProperty("/session");
			if (!oSession.loginRequired) {
				return;
			}
			oModel.setProperty("/session/authenticated", false);
			oModel.setProperty("/session/username", "");
			oModel.setProperty("/session/displayName", "");
			oModel.setProperty("/session/roles", []);
			oModel.setProperty("/session/rolesText", "");
			oModel.setProperty("/session/permissions", { viewOnly: true });
			this.getRouter().navTo("login", {}, true);
		},

		/**
		 * The starting state, and the state after signing out. Nothing is
		 * permitted until the server has said otherwise.
		 */
		_anonymousSession: function () {
			return {
				loaded: false,
				mode: "none",
				loginRequired: false,
				authenticated: false,
				username: "",
				displayName: "",
				roles: [],
				rolesText: "",
				mustChangePassword: false,
				permissions: {
					viewOnly: true,
					editMasterData: false,
					editPlan: false,
					postMovements: false,
					manageUsers: false,
					overrideCapacityBlocks: false
				}
			};
		}
	});
});
